package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type FolderHashEntry struct {
	TurnNumber int    `json:"TurnNumber"`
	SaveCount  int    `json:"SaveCount"`
	TrnHash    uint64 `json:"TrnHash"`
	TwoHHash   uint64 `json:"TwoHHash"`
}

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
		case "2":
			runDaemonMode_lite()
		case "3":
			fmt.Println("Exiting...")
		default:
			fmt.Println("Invalid choice, try again.")
		}
	}
}

func runInstallMode() {
	fmt.Println("Running install mode...")
	appData := setupAppDataFolder()
	reader := bufio.NewReader(os.Stdin)
	choice, backupDest, sourcePath, err := getUserInput(reader, appData)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	writeConfig(appData, choice, backupDest, sourcePath)
	copyExeToAppData(appData)
	createEmptyHashFile(appData)

	fmt.Println("Installed successfully! Daemon starting...")
	runDaemonMode_lite()
}

func runDaemonMode_lite() {
	fmt.Print("DO NOT CLOSE THIS PROCESS. THIS WINDOW MUST STAY OPEN TO USE. \n\n")

	appData := setupAppDataFolder()
	logFilePath, f := openLogFile(appData)
	defer f.Close()
	writeLog("DomFrog daemon started.", f)

	mode, destination, source, err := readConfig(appData)
	if err != nil {
		writeLog("Failed to read config: "+err.Error(), f)
	} else {
		writeLog(fmt.Sprintf("Config loaded: Mode=%s, Destination=%s, Source=%s", mode, destination, source), f)
	}

	for i := 0; ; i++ {
		time.Sleep(1 * time.Second)

		heartbeat(i + 1)

		if err == nil && i+1%5 == 0 {
			backupGames(mode, appData, destination, source, f)
		}
		if i%3600 == 0 {
			trimlog(logFilePath)
		}
	}
}

// -------------------- Install core Core --------------------

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
	exePath, err := os.Executable()
	if err != nil {
		return err
	}

	dstPath := filepath.Join(appDataFolder, "DomFrog.exe")
	if filepath.Dir(exePath) == appDataFolder {
		return nil
	}

	src, err := os.Open(exePath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	return err
}

func createEmptyHashFile(appDataFolder string) error {
	hashFilePath := filepath.Join(appDataFolder, "hash.json")
	if _, err := os.Stat(hashFilePath); os.IsNotExist(err) {
		emptyJSON := []byte("{}")
		return os.WriteFile(hashFilePath, emptyJSON, 0644)
	}
	return nil
}

func getUserInput(reader *bufio.Reader, appData string) (choice, backupDest, sourcePath string, err error) {
	choice, err = step1BackupMode(reader, appData)
	if err != nil {
		return "", "", "", fmt.Errorf("step1BackupMode error: %w", err)
	}

	backupDest, err = step2BackupDestination(reader, appData)
	if err != nil {
		return "", "", "", fmt.Errorf("step2BackupDestination error: %w", err)
	}

	sourcePath, err = step3SavedGamesFolder(reader, appData)
	if err != nil {
		return "", "", "", fmt.Errorf("step3SavedGamesFolder error: %w", err)
	}

	return choice, backupDest, sourcePath, nil
}

func step1BackupMode(reader *bufio.Reader, appData string) (string, error) {
	fmt.Println("Step 1: Choose backup mode")
	fmt.Println("----------------------------------------")
	fmt.Println("1) Save all changes (default)")
	fmt.Println("2) Save most recent IP and incomplete")
	fmt.Println("3) Disable daemon IP and incomplete")

	var choice string
	for choice == "" {
		input := getDefault(reader, "Enter choice (1 or 3), press Enter for default): ", "Mode", "1", appData)
		switch input {
		case "1", "":
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

func step2BackupDestination(reader *bufio.Reader, appData string) (string, error) {
	destBase := os.Getenv("OneDrive")
	if destBase == "" {
		destBase, _ = os.UserHomeDir()
		destBase = filepath.Join(destBase, "Desktop")
	} else {
		destBase = filepath.Join(destBase, "Desktop")
	}
	defaultDest := filepath.Join(destBase, "DomFrogBackup")
	input := getDefault(reader, fmt.Sprintf("Step 2: Backup folder (Enter=default %s): ", defaultDest), "Destination", defaultDest, appData)

	if err := os.MkdirAll(input, 0755); err != nil {
		return "", fmt.Errorf("failed to create backup folder: %w", err)
	}
	return input, nil
}

func step3SavedGamesFolder(reader *bufio.Reader, appData string) (string, error) {
	defaultPath := filepath.Join(os.Getenv("APPDATA"), "Dominions6", "savedgames")
	input := getDefault(reader, fmt.Sprintf("Step 3: Savedgames folder (Enter=default %s): ", defaultPath), "Source", defaultPath, appData)
	return input, nil
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
	changed := false

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

		staticFiles := []string{"ftherlnd"}
		extensions := []string{".map", ".d6m", ".tga"}
		for _, fileName := range staticFiles {
			srcFile := filepath.Join(srcFolder, fileName)
			if _, err := os.Stat(srcFile); err == nil {
				copyFile(srcFile, filepath.Join(dstTopFolder, fileName))
			}
		}
		for _, ext := range extensions {
			files, _ := filepath.Glob(filepath.Join(srcFolder, "*"+ext))
			for _, file := range files {
				copyFile(file, filepath.Join(dstTopFolder, filepath.Base(file)))
			}
		}

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
			continue
		}

		entryHash.TurnNumber = turnNumber
		entryHash.SaveCount = saveCount
		entryHash.TrnHash = trnHash
		entryHash.TwoHHash = twoHHash
		hashes[entry.Name()] = entryHash
		changed = true

		dstFolder := filepath.Join(dstTopFolder, fmt.Sprintf("Turn%d_%d", turnNumber, saveCount))
		os.MkdirAll(dstFolder, 0755)
		fileEntries, _ := os.ReadDir(srcFolder)
		for _, fEntry := range fileEntries {
			if fEntry.IsDir() {
				continue
			}
			name := fEntry.Name()
			if !strings.HasSuffix(name, ".trn") && !strings.HasSuffix(name, ".2h") {
				continue
			}
			copyFile(filepath.Join(srcFolder, name), filepath.Join(dstFolder, name))
		}
		writeLog(fmt.Sprintf("Copied turn/save files to %s", dstFolder), f)
	}

	if changed {
		newData, _ := json.MarshalIndent(hashes, "", "  ")
		os.WriteFile(hashFile, newData, 0644)
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func hashFiles(pattern string) uint64 {
	h := fnv.New64a()
	files, _ := filepath.Glob(pattern)
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		h.Write([]byte(fmt.Sprintf("%d-%d", info.Size(), info.ModTime().UnixNano())))
	}
	return h.Sum64()
}

func readConfig(appData string) (mode, destination, source string, err error) {
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

func trimlog(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 1000 {
		lines = lines[len(lines)-1000:]
	}

	os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

// -------------------- Helpers --------------------

var spinner = []string{"|", "/", "-", "\\"}

func heartbeat(i int) {
	if i%59 == 0 {
		fmt.Println(" Nerf Yomi")
		return
	}

	spin := spinner[i%len(spinner)]
	fmt.Printf("\r%s", spin)
}

func writeLog(msg string, f *os.File) {
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), msg)
	fmt.Print(line)
	if f != nil {
		fmt.Fprint(f, line)
		f.Sync()
	}
}

func ask(reader *bufio.Reader, prompt, defaultVal string) string {
	fmt.Print(prompt)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultVal
	}
	return input
}
func getDefault(reader *bufio.Reader, prompt, configKey, fallback string, appData string) string {
	mode, destination, source, _ := readConfig(appData)
	var def string
	switch configKey {
	case "Mode":
		if mode != "" {
			def = mode
		} else {
			def = fallback
		}
	case "Destination":
		if destination != "" {
			def = destination
		} else {
			def = fallback
		}
	case "Source":
		if source != "" {
			def = source
		} else {
			def = fallback
		}
	default:
		def = fallback
	}
	return ask(reader, prompt, def)
}
