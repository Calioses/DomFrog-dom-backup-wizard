package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const lockFileName = "DomFrog.lock"

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

// ------------------- Setup -------------------

// ----------------------- Main -----------------------

func main() {
	banner := `
     ______                 ______
     |  _  \                |  ___|
     | | | |___  _ __ ___   | |_ _ __ ___   __ _
     | | | / _ \| '_ ' _ \  |  _| '__/ _ \ / _` + "`" + ` |
     | |/ / (_) | | | | | | | | | | | (_) | (_| |
     |___/ \___/|_| |_| |_| \_| |_|  \___/ \__, |
                                            __/ |
                                           |___/

MIT License
Use, modify, and distribute freely, with credit to Monkeydew — the G.O.A.T.
`
	fmt.Println(banner)

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("Choose an option:")
		fmt.Println("1) Install and run")
		fmt.Println("2) Run daemon mode")
		fmt.Println("3) Exit")
		fmt.Print("Enter choice: ")

		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		switch input {
		case "1":
			runInstallMode()
			return
		case "2":
			runDaemonMode_lite()
			return
		case "3":
			fmt.Println("Exiting...")
			return
		default:
			fmt.Println("Invalid choice, try again.")
		}
	}
}

func runInstallMode() {
	fmt.Println("Running install mode...")
	appData := setupAppDataFolder()
	reader := bufio.NewReader(os.Stdin)
	choice, backupDest, sourcePath, err := getUserInput(reader)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	writeConfig(appData, choice, backupDest, sourcePath)
	copyExeToAppData(appData)
	createEmptyHashFile(appData)
	// createScheduledTask(appData) For
	// createStartupShortcut(appData)

	fmt.Println("Installed successfully! Daemon starting...")
	runDaemonMode_lite()
}

func runDaemonMode_lite() {
	fmt.Print("DO NOT CLOSE THIS PROCESS. THE DAEMON IS IP. \n\n")

	appData := setupAppDataFolder()
	lockPath := filepath.Join(appData, lockFileName)

	if pid, ok := readLock(lockPath); ok && isPidRunning(pid) {
		fmt.Println("Daemon already running with PID", pid)
		return
	}

	writeLock(lockPath)

	_, f := openLogFile(appData)
	defer f.Close()
	writeLog("DomFrog daemon started.", f)

	runDaemonLoop(appData, f)
}

// -------------------- DomFrog Core --------------------

func backupGames(mode, appDataFolder, destination, source string, f *os.File) {
	if mode != "1" {
		return
	}

	if _, err := os.Stat(destination); os.IsNotExist(err) {
		writeLog("Destination folder does not exist: "+destination, f)
		return
	}

	hashFile := filepath.Join(appDataFolder, "hash.json")
	hashes := map[string]FolderHashEntry{}
	data, _ := os.ReadFile(hashFile)
	json.Unmarshal(data, &hashes)

	entries, _ := os.ReadDir(source)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "newlords" {
			continue
		}

		srcFolder := filepath.Join(source, entry.Name())
		dstTopFolder := filepath.Join(destination, entry.Name())
		os.MkdirAll(dstTopFolder, 0755)

		entryHash, exists := hashes[entry.Name()]
		if !exists {
			entryHash = FolderHashEntry{}
		}

		// Copy top-level static files
		staticFiles := []string{"ftherlnd"}
		extensions := []string{".map", ".d6m", ".tga"}
		for _, fileName := range staticFiles {
			srcFile := filepath.Join(srcFolder, fileName)
			if _, err := os.Stat(srcFile); err == nil {
				dstFile := filepath.Join(dstTopFolder, fileName)
				data, _ := os.ReadFile(srcFile)
				os.WriteFile(dstFile, data, 0644)
			}
		}
		for _, ext := range extensions {
			files, _ := filepath.Glob(filepath.Join(srcFolder, "*"+ext))
			for _, file := range files {
				dstFile := filepath.Join(dstTopFolder, filepath.Base(file))
				data, _ := os.ReadFile(file)
				os.WriteFile(dstFile, data, 0644)
			}
		}

		// Compute .trn and .2h hashes
		trnHash := hashFiles(filepath.Join(srcFolder, "*.trn"))
		twoHHash := hashFiles(filepath.Join(srcFolder, "*.2h"))

		turnNumber := entryHash.TurnNumber
		saveCount := entryHash.SaveCount

		if trnHash != entryHash.TrnHash {
			turnNumber++
			saveCount = 0
			writeLog(fmt.Sprintf("[%s] .trn changed, new Turn %d", entry.Name(), turnNumber), f)
		} else if twoHHash != entryHash.TwoHHash {
			saveCount++
			writeLog(fmt.Sprintf("[%s] .2h changed, new Save %d", entry.Name(), saveCount), f)
		} else {
			// writeLog(fmt.Sprintf("[%s] No changes detected", entry.Name()), f) Debug line
			continue
		}

		entryHash.TurnNumber = turnNumber
		entryHash.SaveCount = saveCount
		entryHash.TrnHash = trnHash
		entryHash.TwoHHash = twoHHash
		hashes[entry.Name()] = entryHash

		newData, _ := json.MarshalIndent(hashes, "", "  ")
		os.WriteFile(hashFile, newData, 0644)

		// Create turn/save folder and copy only .trn/.2h files
		dstFolder := filepath.Join(dstTopFolder, fmt.Sprintf("Turn%d_%d", turnNumber, saveCount))
		os.MkdirAll(dstFolder, 0755)
		fileEntries, _ := os.ReadDir(srcFolder)
		for _, fEntry := range fileEntries {
			if fEntry.IsDir() || (!strings.HasSuffix(fEntry.Name(), ".trn") && !strings.HasSuffix(fEntry.Name(), ".2h")) {
				continue
			}
			srcPath := filepath.Join(srcFolder, fEntry.Name())
			dstPath := filepath.Join(dstFolder, fEntry.Name())
			data, _ := os.ReadFile(srcPath)
			os.WriteFile(dstPath, data, 0644)
		}
		writeLog(fmt.Sprintf("Copied turn/save files to %s", dstFolder), f)
	}
}

func hashFiles(pattern string) uint64 {
	h := fnv.New64a()
	files, _ := filepath.Glob(pattern)
	for _, f := range files {
		data, _ := os.ReadFile(f)
		h.Write(data)
	}
	return h.Sum64()
}

type FolderHashEntry struct {
	TurnNumber int    `json:"TurnNumber"`
	SaveCount  int    `json:"SaveCount"`
	TrnHash    uint64 `json:"TrnHash"`
	TwoHHash   uint64 `json:"TwoHHash"`
}

// -------------------- Daemon File --------------------

func runDaemonLoop(logFilePath string, f *os.File) {
	appDataFolder := setupAppDataFolder()
	mode, destination, source, err := readConfig()
	if err != nil {
		writeLog("Failed to read config: "+err.Error(), f)
	} else {
		writeLog(fmt.Sprintf("Config loaded: Mode=%s, Destination=%s, Source=%s", mode, destination, source), f)
	}

	seconds := 0
	for {
		time.Sleep(time.Second)
		seconds++

		if seconds%60 == 0 {
			heartbeat(f)
		}

		if err == nil {
			backupGames(mode, appDataFolder, destination, source, f)
		}

		if seconds%(24*60*60) == 0 {
			cleanLogRolling(logFilePath, 30*24*time.Hour) //TODO come back to this
		}
	}
}

func heartbeat(f *os.File) {
	writeLog("DomFrog Daemon heartbeat...", f)
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
	fmt.Println("3) Disable daemon IP and incomplete")

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
		// case "3":
		// 	choice = "3"
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
