package main

import (
	"encoding/json"
	"strings"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
	"golang.org/x/term"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
    "github.com/charmbracelet/bubbles/spinner"
	"noir/textinput"
	"noir/overlay"
)

func main() {
    steamGames = make(map[string]int)
	physicalWidth, _, _ = term.GetSize(int(os.Stdout.Fd()))

	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

type fetchSuccessMsg []game
type fetchDetailsSuccessMsg details
type fetchErrMsg error
type clearErrorMsg struct{}
type downloadGameSuccess struct{}

type steamGamesResponse struct {
	Applist struct {
		Apps []game `json:"apps"`
	} `json:"applist"`
}

type game struct {
	Appid int `json:"appid"`
	Name string `json:"name"`
}

var steamGames map[string]int

var physicalWidth int

const steamGamesEndpoint = "https://api.steampowered.com/ISteamApps/GetAppList/v0002/"
const steamPerGameEndpoint = "https://store.steampowered.com/api/appdetails?appids="

func getCachedUsername() (string, error) {
	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		var err error
		cacheDir, err = os.UserCacheDir()
		if err != nil {
			return "", err
		}
	}
	cachePath := filepath.Join(cacheDir, "noir", "username")

	info, err := os.Stat(cachePath)
	if err != nil {
	    return "", err
	}
	mtime := info.ModTime()

	if time.Since(mtime) < 7 * 24 * time.Hour {
		if d, err := os.ReadFile(cachePath); err == nil {
			return string(d), nil
		} else {
			return "", err
		}
	}

	return "", err
}

func getGames() tea.Msg {
	var data []byte

	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" {
		var err error
		cacheDir, err = os.UserCacheDir()
		if err != nil {
			return fetchErrMsg(err)
		}
	}
	cachePath := filepath.Join(cacheDir, "noir", "steam_app_list.json")

	info, err := os.Stat(cachePath)
	if err == nil {
		mtime := info.ModTime()

		if time.Since(mtime) < 24 * time.Hour {
			if d, err := os.ReadFile(cachePath); err == nil {
				data = d
			}
		}
	}

	if data == nil {
		req, err := http.NewRequest(http.MethodGet, steamGamesEndpoint, nil)
		if err != nil {
			return fetchErrMsg(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fetchErrMsg(err)
		}
		defer resp.Body.Close()

		data, err = io.ReadAll(resp.Body)
		if err != nil {
			return fetchErrMsg(err)
		}

		if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err == nil {
			os.WriteFile(cachePath, data, 0755)
		}
	}

    var _resp steamGamesResponse

	err = json.Unmarshal(data, &_resp)
	if err != nil {
		return fetchErrMsg(err)
	}

	return fetchSuccessMsg(_resp.Applist.Apps)
}

func getGameInfo(appid int) tea.Msg {
	req, err := http.NewRequest(http.MethodGet, steamPerGameEndpoint + strconv.Itoa(appid), nil)
	if err != nil {
		return fetchErrMsg(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fetchErrMsg(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fetchErrMsg(err)
	}

    var _resp map[string]any
    var ret details

	err = json.Unmarshal(data, &_resp)
	if err != nil {
		return fetchErrMsg(err)
	}
    inner, err := json.Marshal(_resp[strconv.Itoa(appid)])
	if err != nil {
		return fetchErrMsg(err)
	}
	err = json.Unmarshal(inner, &_resp)
	if err != nil {
		return fetchErrMsg(err)
	}
    inner, err = json.Marshal(_resp["data"])
	if err != nil {
		return fetchErrMsg(err)
	}
	err = json.Unmarshal(inner, &ret)
	if err != nil {
		return fetchErrMsg(err)
	}

	return fetchDetailsSuccessMsg(ret)
}

func downloadGame(appid int, platform string, username string) tea.Msg {
    f, err := os.CreateTemp("", "steamcmd-download")
	if err != nil {
		return fetchErrMsg(err)
	}
	defer f.Close()

    cwd, err := os.Getwd()
	if err != nil {
		return fetchErrMsg(err)
	}

	fmt.Fprintf(f, `
@ShutdownOnFailedCommand 1
@NoPromptForPassword 1
@sSteamCmdForcePlatformType %s
force_install_dir %s
login %s
app_update %d validate
quit
`, platform, filepath.Join(cwd, strconv.Itoa(appid)), username, appid)

    cmd := exec.Command("steamcmd", "+runscript", f.Name())
	err = cmd.Run()
	if err != nil {
		return fetchErrMsg(err)
	}

	return downloadGameSuccess{}
}

type view int
const (
	Search view = iota
	Details
)

const (
	MacOS int = iota
	Linux
	Windows
)

type platformSupport struct {
	MacOS bool `json:"mac"`
	Linux bool `json:"linux"`
	Windows bool `json:"windows"`
}

type details struct {
	Appid int `json:"steam_appid"`
	Title string `json:"name"`
	Description string `json:"short_description"`
	Developer []string `json:"developer"`
	ReleaseDate struct {
		ComingSoon bool `json:"coming_soon"`
		Date string `json:"date"`
	} `json:"release_date"`
	Website string `json:"website"`
	Platforms platformSupport `json:"platforms"`
	Unused map[string]any `json:"-"`
}

type model struct {
	// why are there no tagged unions? why are there no enums??
	errorMsg string
    currentView view

	spinner spinner.Model
	waiting bool

	searchTextInput textinput.Model

	details details
	detailsFocusIndex int
	detailsLoginTextInput textinput.Model
	detailsLoginActive bool
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "Game title"
	ti.Prompt = "# "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	ti.Focus()
	ti.CharLimit = 100
	ti.ShowSuggestions = true

	dti := textinput.New()
	dti.Prompt = "Steam username: "
	dti.Focus()
	dti.Width = 20
	dti.CharLimit = 20

	s := spinner.New()
	s.Spinner = spinner.Line
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return model{searchTextInput: ti, spinner: s, detailsLoginTextInput: dti}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(getGames, textinput.Blink, m.spinner.Tick)
}

// Updates

func (m model) SearchUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			if appid, ok := steamGames[m.searchTextInput.Value()]; ok {
				m.currentView = Details
	            return m, (func() tea.Msg {
					return getGameInfo(appid)
				})
			} else {
                m.errorMsg = "invalid input\n"
				cmds = append(cmds, tea.Tick(2*time.Second, func(_ time.Time) tea.Msg {
					return clearErrorMsg{}
				}))
			}
		}
	}

	var cmd tea.Cmd
    m.searchTextInput, cmd = m.searchTextInput.Update(msg)
	cmds = append(cmds, cmd)

    return m, tea.Batch(cmds...)
}

func (m model) DetailsUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {

	var platforms []string

	var numButtons int
	if (m.details.Platforms.MacOS) {
		numButtons++
		platforms = append(platforms, "macos")
	}
	if (m.details.Platforms.Linux) {
		numButtons++
		platforms = append(platforms, "linux")
	}
	if (m.details.Platforms.Windows) {
		numButtons++
		platforms = append(platforms, "windows")
	}

	if m.detailsLoginActive {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.Type {
			case tea.KeyEnter:
				m.detailsLoginActive = false

				var cacheErr error
				cacheDir := os.Getenv("XDG_CACHE_HOME")
				if cacheDir == "" {
					cacheDir, cacheErr = os.UserCacheDir()
				}
				if cacheErr == nil {
					cachePath := filepath.Join(cacheDir, "noir", "username")
					if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err == nil {
						os.WriteFile(cachePath, []byte(m.detailsLoginTextInput.Value()), 0755)
					}
				}

				m.waiting = true
				return m, (func() tea.Msg {
					return downloadGame(m.details.Appid, platforms[m.detailsFocusIndex], m.detailsLoginTextInput.Value())
				})
			}
		}

		var cmd tea.Cmd
		m.detailsLoginTextInput, cmd = m.detailsLoginTextInput.Update(msg)

		return m, cmd
	}

	if !m.waiting {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.Type {
			case tea.KeyEscape:
				m.currentView = Search
				m.detailsFocusIndex = 0
			case tea.KeyRight:
				m.detailsFocusIndex = min(numButtons-1, m.detailsFocusIndex+1)
			case tea.KeyLeft:
				m.detailsFocusIndex = max(0, m.detailsFocusIndex-1)
			case tea.KeyEnter:
				if username, err := getCachedUsername(); err == nil {
					m.waiting = true
					return m, (func() tea.Msg {
						return downloadGame(m.details.Appid, platforms[m.detailsFocusIndex], username)
					})
				} else {
					m.detailsLoginActive = true
				}
			}
		}
	}

    return m, nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		}
	case fetchSuccessMsg:
		var suggestions []string
		for _, r := range msg {
			suggestions = append(suggestions, r.Name)
			steamGames[r.Name] = r.Appid
		}
		m.searchTextInput.SetSuggestions(suggestions)
	case fetchDetailsSuccessMsg:
		m.details = details(msg)
	case clearErrorMsg:
		m.errorMsg = ""
	case downloadGameSuccess:
		m.waiting = false
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	switch m.currentView {
	case Search:
        return m.SearchUpdate(msg)
	case Details:
	    return m.DetailsUpdate(msg)
	}

    return m, nil
}

// Views

func (m model) SearchView() string {
	return fmt.Sprintf(
		"%s\nSelect a game:\n\n%s\n\n",
		// error message
		lipgloss.NewStyle().
			Foreground(lipgloss.Color("red")).
			Bold(true).
			Render(m.errorMsg),
		// textinput
		m.searchTextInput.View(),
	)
}

func (m model) DetailsView() string {
	var buttons[]string

	if (m.details.Platforms.MacOS) {
		buttons = append(buttons, "[ MacOS \ue711 ]")
	}
	if (m.details.Platforms.Linux) {
		buttons = append(buttons, "[ Linux \ue712 ]")
	}
	if (m.details.Platforms.Windows) {
		buttons = append(buttons, "[ Windows \ue70f ]")
	}

	for i, b := range buttons {
		if i == m.detailsFocusIndex {
			buttons[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Render(b)
		} else {
			buttons[i] = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(b)
		}
	}

	background := fmt.Sprintf(
		"%s\n%s\n\n%s\n\n%s\n\n",
		// error message
		lipgloss.NewStyle().
			Foreground(lipgloss.Color("red")).
			Bold(true).
			Render(m.errorMsg),
		// title
		lipgloss.NewStyle().
			Bold(true).
			Underline(true).
			Render(m.details.Title),
		// description
		lipgloss.NewStyle().
			Width(physicalWidth).
			Italic(true).
			Render(m.details.Description),
		// download buttons
        strings.Join(buttons, " | "),
	)

	if m.detailsLoginActive {
		if ret, err := overlay.OverlayCenter(background, lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).Padding(1).Render(m.detailsLoginTextInput.View()), true); err == nil {
			return ret
		}
	} else if m.waiting {
		if ret, err := overlay.OverlayCenter(background, lipgloss.NewStyle().BorderStyle(lipgloss.NormalBorder()).Padding(1).Render(fmt.Sprintf("%s  Downloading...", m.spinner.View())), true); err == nil {
			return ret
		}
	}
    return background
}

func (m model) View() string {
	switch m.currentView {
	case Search:
		return m.SearchView()
	case Details:
		return m.DetailsView()
	}
	return "" // unreachable
}

