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
)

type program struct {
	exit chan struct{}
}

func (p *program) Start(s service.Service) error {
	p.exit = make(chan struct{})
	go p.run()
	return nil
}

func (p *program) run() {
	home, _ := os.UserHomeDir()
	appDataFolder := filepath.Join(home, "AppData", "Roaming", "DomFrog")
	os.MkdirAll(appDataFolder, 0755)

	logFile := openLogFile(appDataFolder)
	if logFile == nil {
		return
	}
	defer logFile.Close()

	iniPath := filepath.Join(appDataFolder, "config.ini")
	data, err := os.ReadFile(iniPath)
	if err != nil {
		writeLog(logFile, fmt.Sprintf("Cannot read ini file: %v", err))
		return
	}

	mode, sourcePath, destPath := "", "", ""
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Mode=") {
			mode = strings.TrimPrefix(line, "Mode=")
		}
		if strings.HasPrefix(line, "Source=") {
			sourcePath = strings.TrimPrefix(line, "Source=")
		}
		if strings.HasPrefix(line, "Destination=") {
			destPath = strings.TrimPrefix(line, "Destination=")
		}
	}

	if mode == "" || sourcePath == "" || destPath == "" {
		writeLog(logFile, "Mode, Source, or Destination not set. Exiting.")
		return
	}

	if mode == "3" {
		writeLog(logFile, "Daemon disabled in config.ini. Exiting.")
		return
	}

	writeLog(logFile, "Service started.")
	writeLog(logFile, "Backup folder: "+destPath)
	writeLog(logFile, "Watching: "+sourcePath)

	<-p.exit
}

func (p *program) Stop(s service.Service) error {
	close(p.exit)
	return nil
}

func main() {
	reader := bufio.NewReader(os.Stdin)

	fmt.Print("Do you want to install and start the DomFrog service? (y/N): ")
	input, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(input)) != "y" {
		fmt.Println("Exiting without installing service.")
		return
	}

	choice, backupDest, sourcePath := getUserInput(reader)
	appDataFolder := setupAppDataFolder()
	if appDataFolder == "" {
		return
	}

	writeConfig(appDataFolder, choice, backupDest, sourcePath)
	copyExeToAppData(appDataFolder)

	svcConfig := &service.Config{
		Name:        "DomFrog",
		DisplayName: "DomFrog Daemon",
		Description: "Backup daemon for Dominions 6 savedgames",
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Println("Failed to create service:", err)
		return
	}

	status, err := s.Status()
	if err != nil || status == service.StatusUnknown {
		if err := s.Install(); err != nil {
			fmt.Println("Failed to install service:", err)
			return
		}
		fmt.Println("Service installed.")
	} else {
		fmt.Println("Service already installed.")
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

	fmt.Println("\n========================================")
	fmt.Println("Backup wizard finished")
	fmt.Println("========================================")
}

var logMu sync.Mutex

func writeLog(logFile *os.File, msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Fprintf(logFile, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
	logFile.Sync()
}

// ----------------------- Supporting functions -----------------------

func getUserInput(reader *bufio.Reader) (choice, backupDest, sourcePath string) {
	choice = step1BackupMode(reader)
	backupDest = step2BackupDestination(reader)
	sourcePath = step3SavedGamesFolder(reader)
	return
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

func writeConfig(appDataFolder, choice, backupDest, sourcePath string) {
	iniPath := filepath.Join(appDataFolder, "config.ini")
	content := fmt.Sprintf("[BackupConfig]\nMode=%s\nDestination=%s\nSource=%s\n",
		choice, backupDest, sourcePath)
	if err := os.WriteFile(iniPath, []byte(content), 0644); err != nil {
		fmt.Println("Failed to write config:", err)
	}
}

func copyExeToAppData(appDataFolder string) {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Println("Failed to get exe path:", err)
		return
	}
	dstPath := filepath.Join(appDataFolder, "DomFrog.exe")

	src, err := os.Open(exePath)
	if err != nil {
		fmt.Println("Failed to open exe:", err)
		return
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		fmt.Println("Failed to create exe in AppData:", err)
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		fmt.Println("Failed to copy exe:", err)
	}
}

func step1BackupMode(reader *bufio.Reader) string {
	fmt.Println("Step 1: Choose backup mode")
	fmt.Println("1) Save all changes (default)")
	fmt.Println("2) Save most recent")
	fmt.Println("3) Disable daemon")
	var choice string
	for choice == "" {
		fmt.Print("Enter choice (1-3, Enter=default): ")
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
	return choice
}

func step2BackupDestination(reader *bufio.Reader) string {
	home, _ := os.UserHomeDir()
	defaultDest := filepath.Join(home, "Desktop", "DomFrogBackup")
	var backupDest string
	for backupDest == "" {
		fmt.Printf("Step 2: Backup folder (Enter=default: %s): ", defaultDest)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			backupDest = defaultDest
		} else {
			backupDest = input
		}
		if err := os.MkdirAll(backupDest, 0755); err != nil {
			fmt.Println("Failed to create folder:", err)
			backupDest = ""
		}
	}
	return backupDest
}

func step3SavedGamesFolder(reader *bufio.Reader) string {
	defaultPath := detectSavedGamesFolder()
	var sourcePath string
	for sourcePath == "" {
		fmt.Printf("Step 3: Savedgames folder (Enter=default: %s): ", defaultPath)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			sourcePath = defaultPath
		} else {
			sourcePath = input
		}
		if info, err := os.Stat(sourcePath); err != nil || !info.IsDir() {
			fmt.Println("Invalid folder. Try again.")
			sourcePath = ""
		}
	}
	return sourcePath
}

func detectSavedGamesFolder() string {
	return filepath.Join(os.Getenv("APPDATA"), "Dominions6", "savedgames")
}
