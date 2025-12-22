package playground

import (
	"fmt"
	"sync"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
)

type InteractiveDisplay struct {
	manifest     *Manifest
	taskUpdateCh chan struct{}
	status       sync.Map
}

type taskUI struct {
	tasks    map[string]string
	spinners map[string]spinner.Model
	style    lipgloss.Style
}

func NewInteractiveDisplay(manifest *Manifest) *InteractiveDisplay {
	i := &InteractiveDisplay{
		manifest:     manifest,
		taskUpdateCh: make(chan struct{}),
	}

	go i.printStatus()
	return i
}

func (i *InteractiveDisplay) HandleUpdate(serviceName string, status TaskStatus) {
	i.status.Store(serviceName, status)

	select {
	case i.taskUpdateCh <- struct{}{}:
	default:
	}
}

func (i *InteractiveDisplay) printStatus() {
	fmt.Print("\033[s")
	lineOffset := 0

	// Get ordered service names from manifest
	orderedServices := make([]string, 0, len(i.manifest.Services))
	for _, svc := range i.manifest.Services {
		orderedServices = append(orderedServices, svc.Name)
	}

	// Initialize UI state
	ui := taskUI{
		tasks:    make(map[string]string),
		spinners: make(map[string]spinner.Model),
		style:    lipgloss.NewStyle(),
	}

	// Initialize spinners for each service
	for _, name := range orderedServices {
		sp := spinner.New()
		sp.Spinner = spinner.Dot
		ui.spinners[name] = sp
	}

	tickSpinner := func(name string) spinner.Model {
		sp := ui.spinners[name]
		sp.Tick()
		ui.spinners[name] = sp
		return sp
	}

	for range i.taskUpdateCh {
		// Clear the previous lines and move cursor up
		if lineOffset > 0 {
			fmt.Printf("\033[%dA", lineOffset)
			fmt.Print("\033[J")
		}

		lineOffset = 0
		// Use ordered services instead of ranging over map
		for _, name := range orderedServices {
			status, ok := i.status.Load(name)
			if !ok {
				status = TaskStatusPending
			}

			var statusLine string
			switch status {
			case TaskStatusStarted, TaskStatusHealthy:
				sp := tickSpinner(name)
				statusLine = ui.style.Foreground(lipgloss.Color("2")).Render(fmt.Sprintf("%s [%s] Running", sp.View(), name))
			case TaskStatusDie:
				statusLine = ui.style.Foreground(lipgloss.Color("1")).Render(fmt.Sprintf("✗ [%s] Failed", name))
			case TaskStatusPulled, TaskStatusPending:
				sp := tickSpinner(name)
				statusLine = ui.style.Foreground(lipgloss.Color("3")).Render(fmt.Sprintf("%s [%s] Pending", sp.View(), name))
			case TaskStatusPulling:
				sp := tickSpinner(name)
				statusLine = ui.style.Foreground(lipgloss.Color("3")).Render(fmt.Sprintf("%s [%s] Pulling", sp.View(), name))
			default:
				panic(fmt.Sprintf("BUG: status '%s' not handled", name))
			}

			fmt.Println(statusLine)
			lineOffset++
		}
	}
}
