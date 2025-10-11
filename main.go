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

func isAdmin() bool {
	sid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	token := windows.Token(0)
	member, _ := token.IsMember(sid)
	return member
}

type program struct {
	exit    chan struct{}
	exitMu  sync.Mutex
	logFile *os.File
	wg      sync.WaitGroup
}

func (p *program) Start(s service.Service) error {
	p.exit = make(chan struct{})
	appData := setupAppDataFolder()
	p.logFile = openLogFile(appData)
	if p.logFile == nil {
		return fmt.Errorf("cannot open log file")
	}

	p.wg.Add(1)
	go p.run()

	go func() {
		<-p.exit
	}()

	return nil
}

func (p *program) run() {
	defer p.wg.Done()
	writeLog(p.logFile, "Service started, heartbeat logging every 20 seconds.")

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.exit:
			writeLog(p.logFile, "Service stopping gracefully.")
			return
		case <-ticker.C:
			writeLog(p.logFile, "Daemon heartbeat...")
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
		p.logFile.Close()
	}
	return nil
}

// ----------------------- Main & support -----------------------

func main() {
	prg := &program{}
	svcConfig := &service.Config{
		Name:        "DomFrog",
		DisplayName: "DomFrog Daemon",
		Description: "Backup daemon for Dominions 6 savedgames",
		Option: service.KeyValue{
			"StartType": "automatic",
		},
	}

	s, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Println("Failed to create service:", err)
		return
	}

	if service.Interactive() {
		if !isAdmin() {
			fmt.Println("Warning: program is not running as Administrator. Features will likely fail.")
		} else {
			fmt.Println("Program running as Administrator.")
		}

		reader := bufio.NewReader(os.Stdin)

		fmt.Print("Do you want to install and start the DomFrog service? (y/N): ")
		input, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(input)) != "y" {
			fmt.Println("Exiting without installing service.")
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

		if err := writeConfig(appData, choice, backupDest, sourcePath); err != nil {
			fmt.Println("Error writing config:", err)
			return
		}

		if err := copyExeToAppData(appData); err != nil {
			fmt.Println("Error copying executable:", err)
			return
		}

		status, err := s.Status()
		if err == nil {
			if status == service.StatusStopped {
				fmt.Println("Service exists but is stopped. Starting it now...")
				if err := s.Start(); err != nil {
					fmt.Println("Failed to start service:", err)
					return
				}
				fmt.Println("Service started.")
				return
			}
		} else if status == service.StatusUnknown {
			if err := s.Install(); err != nil {
				fmt.Println("Failed to install service:", err)
				return
			}
			fmt.Println("Service installed.")
			time.Sleep(2 * time.Second)
		}

		status, _ = s.Status()
		if status != service.StatusRunning {
			if err := s.Start(); err != nil {
				fmt.Println("Failed to start service:", err)
				return
			}
			fmt.Println("Service started.")
		} else {
			fmt.Println("Service already running.")
		}

		select {}
	} else {
		if err := s.Run(); err != nil {
			fmt.Println("Service failed:", err)
		}
	}
}

// ----------------------- Logging -----------------------

var logMu sync.Mutex

func writeLog(logFile *os.File, msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Fprintf(logFile, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
	logFile.Sync()
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
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("Cannot get user home directory:", err)
		return ""
	}
	appDataFolder := filepath.Join(home, "AppData", "Roaming", "DomFrog")
	os.MkdirAll(appDataFolder, 0755)
	return appDataFolder
}

func openLogFile(appDataFolder string) *os.File {
	logFilePath := filepath.Join(appDataFolder, "daemon.log")
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Error opening log file:", err)
		return nil
	}
	return logFile
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
