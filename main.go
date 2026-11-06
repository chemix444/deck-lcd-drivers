package main

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const faqURL = "https://help.steampowered.com/en/faqs/view/6121-ECCD-D643-BAA8"

type driver struct {
	name    string
	url     string
	target  string // file inside the zip that installs it (setup.exe, install.bat, foo.inf)
}

// Used only when Valve's page can't be fetched or its layout changes.
var fallback = []driver{
	{"APU", "https://steamdeck-packages.steamos.cloud/misc/windows/drivers/Aerith_Sephiroth_Windows_Driver_2309131113.zip", "setup.exe"},
	{"Wi-Fi", "https://steamdeck-packages.steamos.cloud/misc/windows/drivers/RTLWlanE_WindowsDriver_2024.0.10.137_Drv_3.00.0039_Win11.L.zip", "install.bat"},
	{"Bluetooth", "https://steamdeck-packages.steamos.cloud/misc/windows/drivers/RTBlueR_FilterDriver_1041.3005_1201.2021_new_L.zip", "installdriver.cmd"},
	{"SD Card reader", "https://steamdeck-packages.steamos.cloud/misc/windows/drivers/BayHub_SD_STOR_installV3.4.01.89_W10W11_logoed_20220228.zip", "setup.exe"},
	{"Audio 1/2", "https://steamdeck-packages.steamos.cloud/misc/windows/drivers/cs35l41-V1.2.1.0.zip", "cs35l41.inf"},
	{"Audio 2/2", "https://steamdeck-packages.steamos.cloud/misc/windows/drivers/NAU88L21_x64_1.0.6.0_WHQL%20-%20DUA_BIQ_WHQL.zip", "NAU88L21.inf"},
}

var (
	storeRe  = regexp.MustCompile(`data-faqstore="([^"]*)"`)
	lineRe   = regexp.MustCompile(`\[url=(https://steamdeck-packages\.steamos\.cloud/[^\]]+\.zip)\]`)
	targetRe = regexp.MustCompile(`(?i)(?:run|click)\s+(?:\[b\]\s*)?([\w.\-]+\.(?:exe|bat|cmd|inf))`)
	labelRe  = regexp.MustCompile(`\[b\]([^\[]+?)\[/b\]`)
)

func fetchDrivers() ([]driver, error) {
	resp, err := http.Get(faqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	m := storeRe.FindSubmatch(page)
	if m == nil {
		return nil, fmt.Errorf("driver data not found on page")
	}
	var store struct {
		Faqs map[string]struct{ Content string } `json:"faqs"`
	}
	if err := json.Unmarshal([]byte(html.UnescapeString(string(m[1]))), &store); err != nil {
		return nil, err
	}

	var content string
	for _, f := range store.Faqs {
		content = f.Content
	}
	start := strings.Index(content, "[b]Steam Deck LCD[/b]")
	if start < 0 {
		return nil, fmt.Errorf("LCD section not found")
	}
	section := content[start+len("[b]Steam Deck LCD[/b]"):]
	// The LCD block ends where the next device heading or horizontal rule begins.
	if end := regexp.MustCompile(`\n\[b\]Steam |\[hr\]`).FindStringIndex(section); end != nil {
		section = section[:end[0]]
	}

	var drivers []driver
	label := ""
	audio := 0
	for _, line := range strings.Split(section, "\n") {
		if l := labelRe.FindStringSubmatch(line); l != nil && strings.Contains(strings.ToLower(l[1]), "driver") {
			label = strings.TrimSuffix(strings.TrimSpace(l[1]), " driver")
			label = strings.TrimSuffix(label, " drivers")
		}
		u := lineRe.FindStringSubmatch(line)
		t := targetRe.FindStringSubmatch(line)
		if u == nil || t == nil {
			continue
		}
		name := label
		if strings.HasPrefix(strings.ToLower(label), "audio") {
			audio++
			name = fmt.Sprintf("Audio %d", audio)
		}
		drivers = append(drivers, driver{name, u[1], t[1]})
	}
	if len(drivers) < 4 {
		return nil, fmt.Errorf("only found %d drivers, page layout may have changed", len(drivers))
	}
	return drivers, nil
}

func download(rawURL, dest string) error {
	resp, err := http.Get(rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", rawURL, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	root := filepath.Clean(dest) + string(os.PathSeparator)
	for _, f := range r.File {
		p := filepath.Join(dest, f.Name)
		if !strings.HasPrefix(p, root) {
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			os.MkdirAll(p, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(p)
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// Valve's zips nest files at varying depths, so search rather than assume a layout.
// The shallowest match wins: the APU zip also has a Bin64/Setup.exe that isn't the entry point.
func find(dir, name string) string {
	var hit string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(d.Name(), name) &&
			(hit == "" || strings.Count(p, string(os.PathSeparator)) < strings.Count(hit, string(os.PathSeparator))) {
			hit = p
		}
		return nil
	})
	return hit
}

func fetch(d driver, work string) (string, error) {
	u, _ := url.Parse(d.url)
	zipName, _ := url.PathUnescape(path.Base(u.Path))
	zipPath := filepath.Join(work, zipName)
	dir := strings.TrimSuffix(zipPath, ".zip")

	fmt.Printf("  downloading %s\n", zipName)
	if err := download(d.url, zipPath); err != nil {
		return "", err
	}
	if err := unzip(zipPath, dir); err != nil {
		return "", err
	}
	target := find(dir, d.target)
	if target == "" {
		return "", fmt.Errorf("%s not found in %s", d.target, zipName)
	}
	return target, nil
}

func install(target string) error {
	var cmd *exec.Cmd
	switch strings.ToLower(filepath.Ext(target)) {
	case ".inf":
		cmd = exec.Command("pnputil", "/add-driver", target, "/install")
	case ".bat", ".cmd":
		cmd = exec.Command("cmd", "/c", target)
	default:
		fmt.Println("  the installer window may need you to click through it")
		cmd = exec.Command(target)
	}
	// The vendor scripts reference their own files with relative paths.
	cmd.Dir = filepath.Dir(target)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func isAdmin() bool {
	return exec.Command("net", "session").Run() == nil
}

func elevate() {
	exe, _ := os.Executable()
	exec.Command("powershell", "-NoProfile", "-Command",
		fmt.Sprintf("Start-Process -FilePath '%s' -Verb RunAs", strings.ReplaceAll(exe, "'", "''"))).Run()
}

func productName() string {
	out, _ := exec.Command("reg", "query", `HKLM\HARDWARE\DESCRIPTION\System\BIOS`, "/v", "SystemProductName").Output()
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func main() {
	in := bufio.NewReader(os.Stdin)
	pause := func() {
		fmt.Print("\npress enter to exit...")
		in.ReadString('\n')
	}

	if !isAdmin() {
		fmt.Println("asking for administrator rights...")
		elevate()
		return
	}

	fmt.Println("Steam Deck LCD driver installer")
	fmt.Println("===============================")

	// Jupiter is the LCD board; Galileo is the OLED, which needs different drivers.
	if p := productName(); !strings.EqualFold(p, "Jupiter") {
		fmt.Printf("\nthis PC reports itself as %q, not a Steam Deck LCD (Jupiter).\n", p)
		fmt.Print("these drivers are only for the LCD model. continue anyway? [y/N] ")
		ans, _ := in.ReadString('\n')
		if !strings.EqualFold(strings.TrimSpace(ans), "y") {
			return
		}
	}

	fmt.Println("\nchecking Valve's support page for the latest driver links...")
	drivers, err := fetchDrivers()
	if err != nil {
		fmt.Printf("couldn't read the page (%v), using built-in links instead\n", err)
		drivers = fallback
	}
	for _, d := range drivers {
		fmt.Printf("  %-15s %s\n", d.name, d.target)
	}

	work, err := os.MkdirTemp("", "deck-lcd-drivers-")
	if err != nil {
		fmt.Println(err)
		pause()
		return
	}
	defer os.RemoveAll(work)

	// Everything is downloaded before anything is installed: the Wi-Fi and
	// Bluetooth installers reset the network adapters, which kills any
	// download that comes after them.
	var failed []string
	targets := make([]string, len(drivers))
	fmt.Println("\ndownloading all drivers first...")
	for i, d := range drivers {
		fmt.Printf("\n[%d/%d] %s\n", i+1, len(drivers), d.name)
		t, err := fetch(d, work)
		if err != nil {
			fmt.Printf("  FAILED: %v\n", err)
			failed = append(failed, d.name)
			continue
		}
		targets[i] = t
	}

	fmt.Println("\ninstalling...")
	for i, d := range drivers {
		if targets[i] == "" {
			continue
		}
		fmt.Printf("\n[%d/%d] %s\n", i+1, len(drivers), d.name)
		if err := install(targets[i]); err != nil {
			fmt.Printf("  FAILED: %v\n", err)
			failed = append(failed, d.name)
			continue
		}
		fmt.Println("  done")
	}

	fmt.Println()
	if len(failed) > 0 {
		fmt.Printf("finished with failures: %s\n", strings.Join(failed, ", "))
		fmt.Println("you can grab those manually from " + faqURL)
	} else {
		fmt.Println("all drivers installed. restart the Deck to finish.")
	}
	pause()
}
