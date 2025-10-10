package main

import (
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
)

func main() {
	a := app.New()
	w := a.NewWindow("Backup Wizard")
	w.Resize(fyne.NewSize(400, 250))

	step := 1
	var saveMode string
	var backupLoc string
	var sourceLoc string

	nextBtn := widget.NewButton("Next", nil)
	content := widget.NewLabel("")

	updateStep := func() {
		switch step {
		case 1:
			saveAll := widget.NewRadioGroup([]string{"Save All", "Save Last"}, func(val string) {
				saveMode = val
			})
			content.SetText("")
			w.SetContent(container.NewVBox(
				widget.NewLabel("Step 1: Choose backup mode"),
				saveAll,
				nextBtn,
			))
		case 2:
			nextBtn.OnTapped = func() { step++; updateStep() }
			btn := widget.NewButton("Select Folder", func() {
				dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
					if uri != nil {
						backupLoc = uri.Path()
					}
				}, w)
			})
			w.SetContent(container.NewVBox(
				widget.NewLabel("Step 2: Select backup destination"),
				btn,
				nextBtn,
			))
		case 3:
			nextBtn.OnTapped = func() {
				dialog.ShowInformation("Done", "Save Mode: "+saveMode+"\nBackup To: "+backupLoc+"\nSource: "+sourceLoc, w)
			}
			btn := widget.NewButton("Select Folder", func() {
				dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
					if uri != nil {
						sourceLoc = uri.Path()
					}
				}, w)
			})
			w.SetContent(container.NewVBox(
				widget.NewLabel("Step 3: Select folder to back up"),
				btn,
				nextBtn,
			))
		}
	}

	nextBtn.OnTapped = func() { step++; updateStep() }
	updateStep()
	w.ShowAndRun()
}
