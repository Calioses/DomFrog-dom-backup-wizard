package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kardianos/service"
	"golang.org/x/sys/windows"
)

type program struct {
	exit    chan struct{}
	wg      sync.WaitGroup
	logFile *os.File
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

// ----------------------- Watchdog Service -----------------------

func (p *program) Start(s service.Service) error {
	p.exit = make(chan struct{})

	folder := setupAppDataFolder()
	if folder == "" {
		fmt.Println("ERROR: AppData folder unavailable, using fallback C:\\Temp\\DomFrog")
		folder = "C:\\Temp\\DomFrog"
		os.MkdirAll(folder, 0755)
	}

	logFilePath := filepath.Join(folder, "daemon.log")
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("WARNING: Failed to open log file, logging to console:", err)
		p.logFile = nil
	} else {
		p.logFile = f
	}

	p.wg.Add(1)
	go p.watchdog(folder)

	writeLog("Watchdog service started. Will ensure DomFrog daemon is running.", p.logFile)
	return nil
}

func (p *program) watchdog(appData string) {
	defer p.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	exePath := filepath.Join(appData, "DomFrog.exe")

	for {
		select {
		case <-p.exit:
			writeLog("Watchdog stopping.", p.logFile)
			return
		case <-ticker.C:
			func() {
				defer func() {
					if r := recover(); r != nil {
						writeLog(fmt.Sprintf("ERROR: Watchdog panic: %v", r), p.logFile)
					}
				}()

				// check if process running
				running := false
				out, _ := exec.Command("tasklist").Output()
				if strings.Contains(string(out), "DomFrog.exe") {
					running = true
				}

				if !running {
					writeLog("DomFrog.exe not running, launching daemon...", p.logFile)
					cmd := exec.Command(exePath, "--daemon")
					if err := cmd.Start(); err != nil {
						writeLog(fmt.Sprintf("ERROR: Failed to start daemon: %v", err), p.logFile)
					} else {
						writeLog("DomFrog daemon launched successfully.", p.logFile)
					}
				} else {
					writeLog("DomFrog.exe already running.", p.logFile)
				}
			}()
		}
	}
}

func (p *program) Stop(s service.Service) error {
	close(p.exit)
	p.wg.Wait()
	if p.logFile != nil {
		p.logFile.Close()
	}
	writeLog("Watchdog service stopped.", nil)
	return nil
}

// ----------------------- Main -----------------------

func main() {
	// If launched with --daemon, run daemon function and exit
	if len(os.Args) > 1 && os.Args[1] == "--daemon" {
		runDaemon()
		return
	}

	prg := &program{}
	svcConfig := &service.Config{
		Name:        "DomFrog",
		DisplayName: "DomFrog Daemon Watchdog",
		Description: "Ensures DomFrog daemon is always running",
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
			fmt.Println("Warning: not running as Administrator. Service install may fail.")
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
			fmt.Println("Error getting input:", err)
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
		writeLog("Service installed successfully.", nil)

		if err := s.Start(); err != nil && !strings.Contains(err.Error(), "already running") {
			fmt.Println("Failed to start service:", err)
			return
		}
		writeLog("Service started successfully. Watchdog running in background.", nil)

		fmt.Println("Interactive setup finished. Exiting interactive console.")
		return
	}

	// Running as service
	if err := s.Run(); err != nil {
		f, _ := os.OpenFile("C:\\Temp\\DomFrogServiceErrors.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if f != nil {
			defer f.Close()
			fmt.Fprintf(f, "[%s] Service failed: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
		}
	}
}

// ----------------------- Daemon -----------------------

func runDaemon() {
	appData := setupAppDataFolder()
	logFilePath := filepath.Join(appData, "daemon.log")
	f, _ := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	defer f.Close()

	writeLog("DomFrog daemon started.", f)

	// Example daemon activity
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		writeLog("DomFrog daemon heartbeat...", f)
		time.Sleep(5 * time.Second)
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
		return "", "", "", fmt.Errorf("step1BackupMode error: %w", err)
	}

	backupDest, err = step2BackupDestination(reader)
	if err != nil {
		return "", "", "", fmt.Errorf("step2BackupDestination error: %w", err)
	}

	sourcePath, err = step3SavedGamesFolder(reader)
	if err != nil {
		return "", "", "", fmt.Errorf("step3SavedGamesFolder error: %w", err)
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
	os.MkdirAll(appDataFolder, 0755)
	return appDataFolder
}

func writeConfig(appDataFolder, choice, backupDest, sourcePath string) error {
	iniPath := filepath.Join(appDataFolder, "config.ini")
	content := fmt.Sprintf("[BackupConfig]\nMode=%s\nDestination=%s\nSource=%s\n",
		choice, backupDest, sourcePath)
	return os.WriteFile(iniPath, []byte(content), 0644)
}

func copyExeToAppData(appDataFolder string) error {
	exePath, _ := os.Executable()
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
