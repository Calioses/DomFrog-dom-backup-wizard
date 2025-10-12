package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kardianos/service"
	"golang.org/x/sys/windows"
)

type program struct {
	exit    chan struct{}
	exitMu  sync.Mutex
	logFile *os.File
	wg      sync.WaitGroup
}

func isAdmin() bool {
	sid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	token := windows.Token(0)
	member, _ := token.IsMember(sid)
	return member
}

// ----------------------- Main & support -----------------------

func (p *program) Start(s service.Service) error {
	p.exit = make(chan struct{})

	folder := setupAppDataFolder()
	if folder == "" {
		fmt.Println("ERROR: AppData folder unavailable, using fallback C:\\Temp\\DomFrog")
		folder = "C:\\Temp\\DomFrog"
		if err := os.MkdirAll(folder, 0755); err != nil {
			fmt.Println("ERROR: Failed to create fallback folder:", err)
			return err
		}
	}

	logFilePath := filepath.Join(folder, "daemon.log")
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("WARNING: Failed to open log file, heartbeats will print to console:", err)
		p.logFile = nil
	} else {
		p.logFile = f
	}

	p.wg.Add(1)
	go p.run()

	writeLog("Daemon background process started. Heartbeats will begin shortly.", p.logFile)
	return nil
}

func (p *program) run() {
	defer p.wg.Done()
	writeLog("Heartbeat logging immediately and every 5 seconds.", p.logFile)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.exit:
			writeLog("Service stopping gracefully.", p.logFile)
			return
		case <-ticker.C:
			// per-heartbeat error logging
			func() {
				defer func() {
					if r := recover(); r != nil {
						writeLog(fmt.Sprintf("ERROR: Heartbeat panic recovered: %v", r), p.logFile)
					}
				}()

				writeLog("Daemon heartbeat...", p.logFile)
			}()
		}
	}
}

func (p *program) Stop(s service.Service) error {
	p.exitMu.Lock()
	defer p.exitMu.Unlock()

	select {
	case <-p.exit:
	default:
		close(p.exit)
	}

	p.wg.Wait()

	if p.logFile != nil {
		if err := p.logFile.Close(); err != nil {
			fmt.Println("WARNING: Failed to close log file:", err)
		}
	}

	writeLog("Daemon stopped.", nil)
	return nil
}

func main() {
	prg := &program{}
	svcConfig := &service.Config{
		Name:        "DomFrog",
		DisplayName: "DomFrog Daemon",
		Description: "Backup daemon for Dominions 6 savedgames",
		Option:      service.KeyValue{"StartType": "automatic"},
		UserName:    os.Getenv("USERNAME") + "@" + os.Getenv("USERDOMAIN"),
	}

	s, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Println("Failed to create service:", err)
		return
	}

	if service.Interactive() {
		if !isAdmin() {
			fmt.Println("Warning: program is not running as Administrator. Service install may fail.")
		}

		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Do you want to install and start the DomFrog service? (y/N): ")
		input, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(input)) != "y" {
			fmt.Println("Exiting.")
			return
		}

		choice, backupDest, sourcePath, err := getUserInput(reader)
		if err != nil {
			fmt.Println("Error getting user input:", err)
			return
		}

		appData := setupAppDataFolder()
		if appData == "" {
			fmt.Println("Failed to create AppData folder. Exiting.")
			return
		}

		writeConfig(appData, choice, backupDest, sourcePath)
		copyExeToAppData(appData)

		if err := s.Install(); err != nil && !strings.Contains(err.Error(), "already exists") {
			fmt.Println("Failed to install service:", err)
			return
		}
		writeLog("Service installed successfully", nil)

		if err := s.Start(); err != nil && !strings.Contains(err.Error(), "already running") {
			fmt.Println("Failed to start service:", err)
			return
		}
		writeLog("Service started successfully. Heartbeats will be logged to daemon.log", nil)

		fmt.Println("Service installed and started. Exiting interactive mode.")
		return
	}

	// Running as service: everything happens in background
	if err := s.Run(); err != nil {
		f, _ := os.OpenFile("C:\\Temp\\DomFrogServiceErrors.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if f != nil {
			defer f.Close()
			fmt.Fprintf(f, "[%s] Service failed: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
		}
	}
}

// ----------------------- Logging -----------------------

func writeLog(msg string, f *os.File) {
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
	fmt.Print(line)
	if f != nil {
		fmt.Fprint(f, line)
		f.Sync()
	}
}

// ----------------------- Supporting functions -----------------------

func getUserInput(reader *bufio.Reader) (choice, backupDest, sourcePath string, err error) {
	choice, err = step1BackupMode(reader)
	if err != nil {
		return "", "", "", fmt.Errorf("error in step1BackupMode: %w", err)
	}

	backupDest, err = step2BackupDestination(reader)
	if err != nil {
		return "", "", "", fmt.Errorf("error in step2BackupDestination: %w", err)
	}

	sourcePath, err = step3SavedGamesFolder(reader)
	if err != nil {
		return "", "", "", fmt.Errorf("error in step3SavedGamesFolder: %w", err)
	}

	return choice, backupDest, sourcePath, nil
}

func setupAppDataFolder() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home := os.Getenv("USERPROFILE")
		if home == "" {
			home = "C:\\Users\\Default"
		}
		appData = filepath.Join(home, "AppData", "Roaming")
	}

	appDataFolder := filepath.Join(appData, "DomFrog")
	if err := os.MkdirAll(appDataFolder, 0755); err != nil {
		fmt.Println("Failed to create AppData folder:", err)
		return ""
	}

	return appDataFolder
}

func writeConfig(appDataFolder, choice, backupDest, sourcePath string) error {
	iniPath := filepath.Join(appDataFolder, "config.ini")
	content := fmt.Sprintf("[BackupConfig]\nMode=%s\nDestination=%s\nSource=%s\n",
		choice, backupDest, sourcePath)
	return os.WriteFile(iniPath, []byte(content), 0644)
}

func copyExeToAppData(appDataFolder string) error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	dstPath := filepath.Join(appDataFolder, "DomFrog.exe")
	if filepath.Dir(exePath) == appDataFolder {
		return nil
	}

	src, _ := os.Open(exePath)
	defer src.Close()
	dst, _ := os.Create(dstPath)
	defer dst.Close()
	io.Copy(dst, src)
	return nil
}

func step1BackupMode(reader *bufio.Reader) (string, error) {
	var choice string
	for choice == "" {
		fmt.Print("Step 1: Choose backup mode (1-3, Enter=default 1): ")
		input, _ := reader.ReadString('\n')
		switch strings.TrimSpace(input) {
		case "", "1":
			choice = "1"
		case "2":
			choice = "2"
		case "3":
			choice = "3"
		default:
			fmt.Println("Invalid choice.")
		}
	}
	return choice, nil
}

func step2BackupDestination(reader *bufio.Reader) (string, error) {
	home, _ := os.UserHomeDir()
	defaultDest := filepath.Join(home, "Desktop", "DomFrogBackup")
	fmt.Printf("Step 2: Backup folder (Enter=default %s): ", defaultDest)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultDest, nil
	}
	return input, nil
}

func step3SavedGamesFolder(reader *bufio.Reader) (string, error) {
	defaultPath := filepath.Join(os.Getenv("APPDATA"), "Dominions6", "savedgames")
	fmt.Printf("Step 3: Savedgames folder (Enter=default %s): ", defaultPath)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultPath, nil
	}
	return input, nil
}
