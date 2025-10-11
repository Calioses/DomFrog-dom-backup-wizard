package main

import (
	"bufio"
	"context"
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
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (p *program) Start(s service.Service) error {
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.wg.Add(1)
	go p.run()
	return nil
}

func (p *program) run() {
	defer p.wg.Done()

	home, _ := os.UserHomeDir()
	appDataFolder := filepath.Join(home, "AppData", "Roaming", "DomFrog")
	if err := os.MkdirAll(appDataFolder, 0755); err != nil {
		fmt.Println("Failed to create AppData folder:", err)
		return
	}

	logFile := openLogFile(appDataFolder)
	if logFile == nil {
		fmt.Println("Cannot open log file, exiting daemon.")
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
	if err := scanner.Err(); err != nil {
		writeLog(logFile, fmt.Sprintf("Error scanning ini file: %v", err))
		return
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

	// Watch loop with context for graceful shutdown
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			writeLog(logFile, "Service stopping gracefully.")
			return
		case <-ticker.C:
			// Add your backup/watch logic here if needed
		}
	}
}

func (p *program) Stop(s service.Service) error {
	p.cancel()
	p.wg.Wait()
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

	choice, backupDest, sourcePath, err := getUserInput(reader)
	if err != nil {
		fmt.Println("Error getting user input:", err)
		return
	}

	appDataFolder := setupAppDataFolder()
	if appDataFolder == "" {
		fmt.Println("Failed to create AppData folder. Exiting.")
		return
	}

	if err := writeConfig(appDataFolder, choice, backupDest, sourcePath); err != nil {
		fmt.Println("Error writing config:", err)
		return
	}

	if err := copyExeToAppData(appDataFolder); err != nil {
		fmt.Println("Error copying executable:", err)
		return
	}

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

// ----------------------- Logging -----------------------
var logMu sync.Mutex

func writeLog(logFile *os.File, msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Fprintf(logFile, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
	_ = logFile.Sync()
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
	if err := os.MkdirAll(appDataFolder, 0755); err != nil {
		fmt.Println("Failed to create AppData folder:", err)
		return ""
	}
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
	if err := os.WriteFile(iniPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("cannot write config.ini: %w", err)
	}
	return nil
}

func copyExeToAppData(appDataFolder string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot get executable path: %w", err)
	}

	dstPath := filepath.Join(appDataFolder, "DomFrog.exe")
	if filepath.Dir(exePath) == appDataFolder {
		return nil // already in place
	}

	src, err := os.Open(exePath)
	if err != nil {
		return fmt.Errorf("cannot open source exe: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("cannot create destination exe: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to copy exe: %w", err)
	}

	return nil
}

func step1BackupMode(reader *bufio.Reader) (string, error) {
	fmt.Println("Step 1: Choose backup mode")
	fmt.Println("1) Save all changes (default)")
	fmt.Println("2) Save most recent")
	fmt.Println("3) Disable daemon")

	var choice string
	for choice == "" {
		fmt.Print("Enter choice (1-3, Enter=default): ")
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
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

	return backupDest, nil
}

func step3SavedGamesFolder(reader *bufio.Reader) (string, error) {
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

		info, err := os.Stat(sourcePath)
		if err != nil || !info.IsDir() {
			fmt.Println("Invalid folder. Try again.")
			sourcePath = ""
		}
	}

	return sourcePath, nil
}

func detectSavedGamesFolder() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		home, _ := os.UserHomeDir()
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(appData, "Dominions6", "savedgames")
}
