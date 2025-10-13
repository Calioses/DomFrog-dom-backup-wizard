package main

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// TODO add second option
// TODO add a check for config vars before asking again

const DETACHED_PROCESS = 0x00000008
const CREATE_NEW_PROCESS_GROUP = 0x00000200
const lockFileName = "DomFrog.lock"

type FolderHashes map[string]uint64

// ------------------- Setup -------------------

func createStartupShortcut(appData string) {
	startup := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Startup")
	os.MkdirAll(startup, 0755)
	shortcut := filepath.Join(startup, "DomFrog.lnk")
	vbs := fmt.Sprintf(`Set WshShell = WScript.CreateObject("WScript.Shell")
		Set shortcut = WshShell.CreateShortcut("%s")
		shortcut.TargetPath = "%s"
		shortcut.Arguments = "--daemon"
		shortcut.WorkingDirectory = "%s"
		shortcut.WindowStyle = 0
		shortcut.Save`, shortcut, filepath.Join(appData, "DomFrog.exe"), appData)
	tmp := filepath.Join(os.TempDir(), "shortcut.vbs")
	os.WriteFile(tmp, []byte(vbs), 0644)
	defer os.Remove(tmp)
	exec.Command("wscript", tmp).Run()
}

// ----------------------- Main -----------------------
// TODO make the daemon launch in detatched instead of visible

func main() {
	// detect if running as daemon
	if len(os.Args) > 1 && os.Args[1] == "--daemon" {
		runDaemonForever()
		return
	}

	// interactive installer (first run)
	reader := bufio.NewReader(os.Stdin)
	choice, backupDest, sourcePath, err := getUserInput(reader)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	appData := setupAppDataFolder()
	writeConfig(appData, choice, backupDest, sourcePath)
	copyExeToAppData(appData)
	createEmptyHashFile(appData)
	createScheduledTask(appData)
	createStartupShortcut(appData)

	fmt.Println("Installed successfully! Daemon starting...")
	launchDetachedDaemon(appData)
}

// -------------------- DomFrog Core --------------------

func getSubfolders(source string) ([]string, error) {
	entries, err := os.ReadDir(source)
	if err != nil {
		return nil, err
	}

	var folders []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "newlords" {
			folders = append(folders, entry.Name())
		}
	}
	return folders, nil
}

func hashFolder(folderPath string) (uint64, error) {
	h := fnv.New64a()

	err := filepath.WalkDir(folderPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			data, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			h.Write(data)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return h.Sum64(), nil
}

func StoreFolderHash(appDataFolder, folderName, folderPath string) error {
	hashFile := filepath.Join(appDataFolder, "hash.json")

	hashes := FolderHashes{}
	if data, err := ioutil.ReadFile(hashFile); err == nil {
		json.Unmarshal(data, &hashes)
	}

	folderHash, err := hashFolder(folderPath)
	if err != nil {
		return err
	}

	hashes[folderName] = folderHash

	newData, _ := json.MarshalIndent(hashes, "", "  ")
	return ioutil.WriteFile(hashFile, newData, 0644)
}

func logAndCopySubfolders(mode, destination, source string, f *os.File) {
	if mode != "1" {
		return
	}

	if _, err := os.Stat(destination); os.IsNotExist(err) {
		writeLog("Destination folder does not exist: "+destination, f)
		return
	}

	sourceList, err := getSubfolders(source)
	if err != nil {
		writeLog("Failed to read source folder: "+err.Error(), f)
		return
	}

	turnNumber := 1

	for _, folderName := range sourceList {
		srcFolder := filepath.Join(source, folderName)
		dstParent := filepath.Join(destination, folderName)
		os.MkdirAll(dstParent, 0755)

		saveNumber := 0
		var dstFolder, dstZip string
		for {
			dstFolder = filepath.Join(dstParent, fmt.Sprintf("%s_Turn%d_save%d", folderName, turnNumber, saveNumber))
			dstZip = dstFolder + ".zip"

			if fileExists(dstZip) {
				writeLog(fmt.Sprintf("Zip already exists: %s", dstZip), f)
				break
			}

			if fileExists(dstFolder) {
				// Folder exists but not zipped → zip it and skip creating a new save
				writeLog(fmt.Sprintf("Partial folder exists, zipping: %s", dstFolder), f)
				if err := zipFolder(dstFolder); err != nil {
					writeLog(fmt.Sprintf("Failed to zip %s: %v", dstFolder, err), f)
				} else {
					writeLog(fmt.Sprintf("Zipped %s → %s.zip", dstFolder, dstFolder), f)
				}
				break
			}

			// Neither zip nor folder exists → create new save
			os.MkdirAll(dstFolder, 0755)
			if err := copyFolderContents(srcFolder, dstFolder); err != nil {
				writeLog(fmt.Sprintf("Failed to copy %s → %s: %v", srcFolder, dstFolder, err), f)
			} else {
				writeLog(fmt.Sprintf("Copied %s → %s", srcFolder, dstFolder), f)
				if err := zipFolder(dstFolder); err != nil {
					writeLog(fmt.Sprintf("Failed to zip %s: %v", dstFolder, err), f)
				} else {
					writeLog(fmt.Sprintf("Zipped %s → %s.zip", dstFolder, dstFolder), f)
				}
			}
			break
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFolderContents(src, dst string) error {
	os.MkdirAll(dst, 0755)
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			os.MkdirAll(dstPath, 0755)
			if err := copyFolderContents(srcPath, dstPath); err != nil {
				return err
			}
			if err := zipFolder(dstPath); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				return err
			}
		}
	}
	return nil
}

func zipFolder(parentFolder string) error {
	dstZip := parentFolder + ".zip"

	if err := zipFolderContentsOnly(parentFolder, dstZip); err != nil {
		return err
	}

	return os.RemoveAll(parentFolder)
}

func zipFolderContentsOnly(srcFolder, dstZip string) error {
	zipFile, err := os.Create(dstZip)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	return filepath.Walk(srcFolder, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == srcFolder {
			return nil
		}

		relPath, err := filepath.Rel(srcFolder, path)
		if err != nil {
			return err
		}

		if info.IsDir() {
			_, err = zipWriter.Create(relPath + "/")
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		w, err := zipWriter.Create(relPath)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, f)
		return err
	})
}

// -------------------- Daemon File --------------------

func runDaemonLoop(logFilePath string, f *os.File) {
	heartbeatTicker := time.NewTicker(10 * time.Second)
	defer heartbeatTicker.Stop()

	cleanupTicker := time.NewTicker(24 * time.Hour)
	defer cleanupTicker.Stop()

	mode, destination, source, err := readConfig()
	if err != nil {
		writeLog("Failed to read config: "+err.Error(), f)
	} else {
		writeLog(fmt.Sprintf("Config loaded: Mode=%s, Destination=%s, Source=%s", mode, destination, source), f)
	}

	for {
		select {
		case <-heartbeatTicker.C:
			heartbeat(f)
			if err == nil {
				logAndCopySubfolders(mode, destination, source, f)
			}
		case <-cleanupTicker.C:
			cleanLogRolling(logFilePath, 30*24*time.Hour)
		}
	}
}

func heartbeat(f *os.File) {
	writeLog("Daemon heartbeat...", f)
}

func readConfig() (mode, destination, source string, err error) {
	appData := setupAppDataFolder()
	iniPath := filepath.Join(appData, "config.ini")

	data, err := os.ReadFile(iniPath)
	if err != nil {
		return "", "", "", err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Mode=") {
			mode = strings.TrimPrefix(line, "Mode=")
		} else if strings.HasPrefix(line, "Destination=") {
			destination = strings.TrimPrefix(line, "Destination=")
		} else if strings.HasPrefix(line, "Source=") {
			source = strings.TrimPrefix(line, "Source=")
		}
	}

	return mode, destination, source, nil
}

func runDaemonForever() {
	appData := setupAppDataFolder()
	lockPath := filepath.Join(appData, lockFileName)

	if tryLaunchDetached(appData) {
		return
	}

	if checkExistingDaemon(lockPath) {
		return
	}
	writeLock(lockPath)

	logFilePath, f := openLogFile(appData)
	defer f.Close()

	writeLog("DomFrog daemon started.", f)

	runDaemonLoop(logFilePath, f)
}

func tryLaunchDetached(appData string) bool {
	if len(os.Args) == 1 {
		if err := launchDetachedDaemon(appData); err != nil {
			fmt.Println("Failed to launch detached daemon:", err)
		}
		return true
	}
	return false
}

func checkExistingDaemon(lockPath string) bool {
	if pid, ok := readLock(lockPath); ok && isPidRunning(pid) {
		fmt.Println("Daemon already running with PID", pid)
		return true
	}
	return false
}

func openLogFile(appData string) (string, *os.File) {
	logFilePath := filepath.Join(appData, "daemon.log")
	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Failed to open daemon.log:", err)
		os.Exit(1)
	}
	return logFilePath, f
}

func cleanLogRolling(path string, maxAge time.Duration) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	now := time.Now()
	var kept []string

	for _, line := range lines {
		if line == "" {
			continue
		}
		if len(line) < 21 || line[0] != '[' || line[20] != ']' {
			kept = append(kept, line)
			continue
		}
		ts, err := time.Parse("2006-01-02 15:04:05", line[1:20])
		if err != nil {
			kept = append(kept, line)
			continue
		}
		if now.Sub(ts) <= maxAge {
			kept = append(kept, line)
		}
	}

	os.WriteFile(path, []byte(strings.Join(kept, "\n")+"\n"), 0644)
}

func launchDetachedDaemon(appData string) error {
	exePath := filepath.Join(appData, "DomFrog.exe")
	cmd := exec.Command(exePath, "--daemon")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP,
	}
	return cmd.Start()
}

// -------------------- Lock File --------------------

func writeLock(path string) {
	pid := os.Getpid()
	os.WriteFile(path, []byte(strconv.Itoa(pid)), 0644)
}

func readLock(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

func isPidRunning(pid int) bool {
	cmd := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid))
	out, _ := cmd.Output()
	return !strings.Contains(strings.ToLower(string(out)), "no tasks")
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

func createEmptyHashFile(appDataFolder string) error {
	hashFilePath := filepath.Join(appDataFolder, "hash.json")
	if _, err := os.Stat(hashFilePath); os.IsNotExist(err) {
		emptyJSON := []byte("{}")
		return os.WriteFile(hashFilePath, emptyJSON, 0644)
	}
	return nil
}

func step1BackupMode(reader *bufio.Reader) (string, error) {
	fmt.Println("Step 1: Choose backup mode")
	fmt.Println("----------------------------------------")
	fmt.Println("1) Save all changes (default)")
	fmt.Println("2) Save most recent IP and incomplete")
	fmt.Println("3) Disable daemon")

	var choice string
	for choice == "" {
		fmt.Print("Enter choice (1 or 3), press Enter for default): ")
		input, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("failed to read input: %w", err)
		}
		input = strings.TrimSpace(input)
		switch input {
		case "", "1":
			choice = "1"
		// case "2":
		// 	choice = "2"
		case "3":
			choice = "3"
		default:
			fmt.Println("Invalid choice, try again.")
		}
	}

	fmt.Println("You selected option number:", choice)
	fmt.Println()
	return choice, nil
}

func step2BackupDestination(reader *bufio.Reader) (string, error) {
	home, _ := os.UserHomeDir()
	oneDrive := os.Getenv("OneDrive")
	var defaultDest string

	if oneDrive != "" {
		defaultDest = filepath.Join(oneDrive, "Desktop", "DomFrogBackup")
	} else {
		defaultDest = filepath.Join(home, "Desktop", "DomFrogBackup")
	}

	fmt.Printf("Step 2: Backup folder (Enter=default %s): ", defaultDest)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		input = defaultDest
	}

	if err := os.MkdirAll(input, 0755); err != nil {
		return "", fmt.Errorf("failed to create backup folder: %w", err)
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

func createScheduledTask(appData string) {
	exePath := filepath.Join(appData, "DomFrog.exe")
	taskName := "DomFrogDaemon"

	cmd := exec.Command("schtasks",
		"/Create",
		"/F", // force overwrite
		"/RL", "HIGHEST",
		"/SC", "ONLOGON",
		"/TN", taskName,
		"/TR", fmt.Sprintf("\"%s\" --daemon", exePath),
	)
	_ = cmd.Run()
}
