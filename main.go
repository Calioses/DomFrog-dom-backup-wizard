package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

/*
     ______                 ______
     |  _  \                |  ___|
     | | | |___  _ __ ___   | |_ _ __ ___   __ _
     | | | / _ \| '_ ` _ \  |  _| '__/ _ \ / _` |
     | |/ / (_) | | | | | | | | | | | (_) | (_| |
     |___/ \___/|_| |_| |_| \_| |_|  \___/ \__, |
                                            __/ |
                                           |___/

MIT License
Use, modify, and distribute freely, with credit to Monkeydew — the G.O.A.T.

*/

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--daemon" {
		runDaemonMode()
		return
	}

	reader := bufio.NewReader(os.Stdin)
	choice, backupDest, sourcePath := getUserInput(reader)

	appDataFolder := setupAppDataFolder()
	if appDataFolder == "" {
		return
	}

	logFile := openLogFile(appDataFolder)
	if logFile == nil {
		return
	}
	defer logFile.Close()

	writeConfig(appDataFolder, choice, backupDest, sourcePath)
	copyExeToAppData(appDataFolder)
	manageStartupDaemon(appDataFolder)

	fmt.Println("\n========================================")
	fmt.Println("Backup wizard finished")
	fmt.Println("========================================")
}

func getUserInput(reader *bufio.Reader) (choice, backupDest, sourcePath string) {
	fmt.Println("========================================")
	fmt.Println("      DOMINIONS 6 BACKUP WIZARD")
	fmt.Println("========================================")
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
	content := fmt.Sprintf("[BackupConfig]\n# 1=Save all changes, 2=Save most recent, 3=Disable daemon\nMode=%s\nDestination=%s\nSource=%s\n",
		choice, backupDest, sourcePath)
	if err := os.WriteFile(iniPath, []byte(content), 0644); err != nil {
		fmt.Println("Error writing config.ini:", err)
	} else {
		fmt.Println("Configuration saved to:", iniPath)
	}
}

func copyExeToAppData(appDataFolder string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot get executable path: %w", err)
	}

	copyPath := filepath.Join(appDataFolder, "DomFrog.exe")

	src, err := os.Open(exePath)
	if err != nil {
		return fmt.Errorf("cannot open source exe: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(copyPath) // os.Create truncates if file exists
	if err != nil {
		return fmt.Errorf("cannot create exe copy: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("error copying exe: %w", err)
	}

	fmt.Println("Executable copied to AppData:", copyPath)
	return nil
}

func step1BackupMode(reader *bufio.Reader) string {
	fmt.Println("Step 1: Choose backup mode")
	fmt.Println("----------------------------------------")
	fmt.Println("1) Save all changes (default)")
	fmt.Println("2) Save most recent")
	fmt.Println("3) Disable daemon")

	var choice string
	for choice == "" {
		fmt.Print("Enter choice (1-3, press Enter for default): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		switch input {
		case "", "1":
			choice = "1"
		case "2":
			choice = "2"
		case "3":
			choice = "3"
		default:
			fmt.Println("Invalid choice, try again.")
		}
	}

	fmt.Println("You selected option number:", choice)
	fmt.Println()
	return choice
}

func step2BackupDestination(reader *bufio.Reader) string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("Cannot get user home directory:", err)
		return ""
	}

	// Suggested default location for backups
	onedriveDesktop := filepath.Join(home, "OneDrive", "Desktop", "DomFrogBackup")
	normalDesktop := filepath.Join(home, "Desktop", "DomFrogBackup")

	defaultBackup := normalDesktop
	if _, err := os.Stat(onedriveDesktop); err == nil {
		defaultBackup = onedriveDesktop
	}

	fmt.Println("Step 2: Select DomFrog backup folder")
	fmt.Println("----------------------------------------")
	fmt.Printf("Press Enter to use default: %s\n", defaultBackup)

	var backupDest string
	for backupDest == "" {
		fmt.Print("Enter folder path (or press Enter to use default): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" && defaultBackup != "" {
			backupDest = defaultBackup
		} else if input != "" {
			backupDest = filepath.Join(input, "DomFrogBackup")
		}

		if err := os.MkdirAll(backupDest, 0755); err != nil {
			fmt.Println("Could not create folder:", err)
			backupDest = ""
		}
	}

	fmt.Println("DomFrog backup folder set to:", backupDest)
	fmt.Println()
	return backupDest
}

func step3SavedGamesFolder(reader *bufio.Reader) string {
	fmt.Println("Step 3: Select Dominions6 savedgames folder")
	fmt.Println("----------------------------------------")
	autoPath := detectSavedGamesFolder()
	if autoPath != "" {
		fmt.Println("Autodetected savedgames folder:", autoPath)
	} else {
		fmt.Println("No savedgames folder detected automatically.")
	}

	var sourcePath string
	for sourcePath == "" {
		fmt.Print("Enter folder path (press Enter to use autodetected path): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" && autoPath != "" {
			sourcePath = autoPath
		} else if input != "" {
			sourcePath = input
		}
		if info, err := os.Stat(sourcePath); err != nil || !info.IsDir() {
			fmt.Println("Invalid folder, try again.")
			sourcePath = ""
		}
	}
	fmt.Println("Savedgames folder selected:", sourcePath)
	fmt.Println()
	return sourcePath
}

func detectSavedGamesFolder() string {
	home, _ := os.UserHomeDir()
	var path string

	switch runtime.GOOS {
	case "windows":
		path = filepath.Join(os.Getenv("APPDATA"), "Dominions6", "savedgames")
	case "darwin":
		path = filepath.Join(home, "Library", "Application Support", "Dominions6", "savedgames")
	default:
		path = filepath.Join(home, ".local", "share", "Dominions6", "savedgames")
	}

	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return path
	}
	return ""
}

func manageStartupDaemon(appDataFolder string) error {
	logFile := openLogFile(appDataFolder)
	if logFile == nil {
		return fmt.Errorf("cannot open log file")
	}
	defer logFile.Close()

	iniPath := filepath.Join(appDataFolder, "config.ini")
	data, err := os.ReadFile(iniPath)
	if err != nil {
		fmt.Fprintf(logFile, "[%s] Cannot read ini file: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
		return err
	}

	mode := ""
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "Mode=") {
			mode = strings.TrimPrefix(line, "Mode=")
			break
		}
	}

	exePath := filepath.Join(appDataFolder, "DomFrog.exe")
	if _, err := os.Stat(exePath); err != nil {
		fmt.Fprintf(logFile, "[%s] DomFrog.exe not found in AppData: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
		return err
	}

	startupDir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	shortcutPath := filepath.Join(startupDir, "DomFrog.lnk")

	if mode == "3" {
		if _, err := os.Stat(shortcutPath); err == nil {
			os.Remove(shortcutPath)
			fmt.Fprintf(logFile, "[%s] Removed startup shortcut (daemon disabled)\n", time.Now().Format("2006-01-02 15:04:05"))
		}
		return nil
	}

	if _, err := os.Stat(shortcutPath); err == nil {
		os.Remove(shortcutPath)
	}

	vbs := fmt.Sprintf(`Set WshShell = WScript.CreateObject("WScript.Shell")
Set shortcut = WshShell.CreateShortcut("%s")
shortcut.TargetPath = "%s"
shortcut.Arguments = "--daemon"
shortcut.WorkingDirectory = "%s"
shortcut.WindowStyle = 0
shortcut.Save`, shortcutPath, exePath, filepath.Dir(exePath))

	tmpFile := filepath.Join(os.TempDir(), "create_shortcut.vbs")
	if err := os.WriteFile(tmpFile, []byte(vbs), 0644); err != nil {
		fmt.Fprintf(logFile, "[%s] Error writing VBS file: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
		return err
	}
	defer os.Remove(tmpFile)

	if _, err := exec.LookPath("wscript"); err != nil {
		fmt.Fprintf(logFile, "[%s] wscript not found: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
		return err
	}

	cmd := exec.Command("wscript", tmpFile)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(logFile, "[%s] Failed to create shortcut: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
		return err
	}

	fmt.Fprintf(logFile, "[%s] Startup shortcut created successfully: %s\n", time.Now().Format("2006-01-02 15:04:05"), shortcutPath)
	return nil
}

func HashFile(path string) uint64 {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var h uint64
	for _, b := range data {
		h = h*31 + uint64(b)
	}
	return h
}

var logMu sync.Mutex

func writeLog(logFile *os.File, msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Fprintf(logFile, "[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
	logFile.Sync()
}

func runDaemonMode() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

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
		fmt.Fprintf(logFile, "[%s] Cannot read ini file: %v\n", time.Now().Format("2006-01-02 15:04:05"), err)
		logFile.Sync()
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

	if mode == "" || sourcePath == "" {
		fmt.Fprintf(logFile, "[%s] Mode or Source not set in config.ini. Exiting.\n", time.Now().Format("2006-01-02 15:04:05"))
		logFile.Sync()
		return
	}

	if mode == "3" {
		fmt.Fprintf(logFile, "[%s] Daemon is disabled in config.ini. Exiting.\n", time.Now().Format("2006-01-02 15:04:05"))
		logFile.Sync()
		return
	}

	writeLog(logFile, "Daemon started.")
	writeLog(logFile, "Backup folder: "+destPath)
	writeLog(logFile, "Watching savedgames folder: "+sourcePath)

	// Heartbeat goroutine
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			writeLog(logFile, "Daemon heartbeat.")
		}
	}()

	// Keep the daemon running (replace with your folder watch logic)
	select {}
}
