package ytstudio

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

// ChromedpDriver is the production Driver. Construct with NewChromedpDriver.
type ChromedpDriver struct {
	ProfileRoot string // directory containing one subdir per profile_name
	BrowserExe  string // optional explicit path; empty => autodetect
	Verbose     bool
}

// NewChromedpDriver returns a driver that stores profiles under profileRoot.
// If browserExe is "" the driver searches the usual install locations for
// Brave, then Edge, then Chrome.
func NewChromedpDriver(profileRoot, browserExe string) *ChromedpDriver {
	return &ChromedpDriver{ProfileRoot: profileRoot, BrowserExe: browserExe}
}

func (d *ChromedpDriver) logf(format string, args ...any) {
	if d.Verbose {
		log.Printf("ytstudio: "+format, args...)
	}
}

// findBrowser returns an absolute path to a Brave/Edge/Chrome binary.
func (d *ChromedpDriver) findBrowser() (string, error) {
	if d.BrowserExe != "" {
		return d.BrowserExe, nil
	}
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		pf := os.Getenv("ProgramFiles")
		pf86 := os.Getenv("ProgramFiles(x86)")
		local := os.Getenv("LOCALAPPDATA")
		candidates = []string{
			filepath.Join(pf, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			filepath.Join(pf86, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			filepath.Join(local, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			filepath.Join(pf, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(pf86, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(pf, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(pf86, "Google", "Chrome", "Application", "chrome.exe"),
		}
	case "darwin":
		candidates = []string{
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}
	default:
		candidates = []string{
			"/usr/bin/brave-browser",
			"/usr/bin/brave",
			"/opt/brave.com/brave/brave",
			"/snap/bin/brave",
			"/usr/bin/microsoft-edge",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("ytstudio: no Brave/Edge/Chrome binary found (set browser_exe in config)")
}

// allocate returns a chromedp ExecAllocator bound to the given profile.
// Callers must defer the returned cancel func.
func (d *ChromedpDriver) allocate(ctx context.Context, profileName string, headless bool) (context.Context, context.CancelFunc, error) {
	if profileName == "" {
		return nil, nil, errors.New("ytstudio: profile_name is empty")
	}
	profileDir := filepath.Join(d.ProfileRoot, profileName)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return nil, nil, err
	}
	// Remove the SingletonLock left behind by an interrupted previous run.
	// chromedp's "cannot start, profile in use" failures all come back to
	// this file.
	_ = os.Remove(filepath.Join(profileDir, "SingletonLock"))

	exe, err := d.findBrowser()
	if err != nil {
		return nil, nil, err
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(exe),
		chromedp.UserDataDir(profileDir),
		chromedp.NoSandbox,
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("no-restore-last-session", true),
		chromedp.Flag("restore-last-session", "false"),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1400, 900),
	}
	if headless {
		opts = append(opts, chromedp.Headless)
	} else {
		opts = append(opts, chromedp.Flag("headless", false))
	}
	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	return allocCtx, cancel, nil
}

// detectSignIn checks whether the current page URL indicates a sign-in
// redirect. Any URL on accounts.google.com or anything containing "/signin"
// signals an expired session.
func detectSignIn(currentURL string) bool {
	u := strings.ToLower(currentURL)
	return strings.Contains(u, "accounts.google") || strings.Contains(u, "/signin")
}

// Login opens YouTube Studio non-headless and blocks until the operator
// closes the browser window. The profile cookies persist after close.
func (d *ChromedpDriver) Login(ctx context.Context, profileName string) error {
	deadline := DefaultLoginDeadline
	ctx, cancelTO := context.WithTimeout(ctx, deadline)
	defer cancelTO()

	allocCtx, cancelAlloc, err := d.allocate(ctx, profileName, false)
	if err != nil {
		return err
	}
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	if err := chromedp.Run(browserCtx,
		chromedp.Navigate("https://studio.youtube.com"),
	); err != nil {
		return err
	}
	d.logf("login: browser open, waiting for operator to close")
	// chromedp.NewContext registers a target-detached handler; when the
	// operator closes the window, browserCtx.Done() fires.
	<-browserCtx.Done()
	return nil
}

// CheckChannel opens YT Studio headlessly and reads the channel name. Returns
// ErrSessionExpired when the profile no longer has a session.
func (d *ChromedpDriver) CheckChannel(ctx context.Context, profileName string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	allocCtx, cancelAlloc, err := d.allocate(ctx, profileName, true)
	if err != nil {
		return "", err
	}
	defer cancelAlloc()
	bctx, cancelB := chromedp.NewContext(allocCtx)
	defer cancelB()

	var currentURL, channelName string
	err = chromedp.Run(bctx,
		chromedp.Navigate("https://studio.youtube.com"),
		chromedp.Sleep(2*time.Second),
		chromedp.Location(&currentURL),
		chromedp.Evaluate(jsReadChannelName, &channelName),
	)
	if err != nil {
		return "", err
	}
	if detectSignIn(currentURL) {
		return "", ErrSessionExpired
	}
	channelName = strings.TrimSpace(channelName)
	return channelName, nil
}

// Upload performs the full upload: open Studio, click Create, attach the
// file via setInputFiles on the hidden <input type=file>, fill title and
// description, optionally set thumbnail, wait for "Checks complete", set
// visibility, extract the 11-char ID, Save, optionally add to playlist.
func (d *ChromedpDriver) Upload(ctx context.Context, profileName string, in UploadInput) (UploadResult, error) {
	ctx, cancelTO := context.WithTimeout(ctx, DefaultUploadDeadline)
	defer cancelTO()

	allocCtx, cancelAlloc, err := d.allocate(ctx, profileName, false)
	if err != nil {
		return UploadResult{}, err
	}
	defer cancelAlloc()
	bctx, cancelB := chromedp.NewContext(allocCtx)
	defer cancelB()

	visibility := strings.ToUpper(strings.TrimSpace(in.Visibility))
	if visibility == "" {
		visibility = "PUBLIC"
	}

	abs, err := filepath.Abs(in.VideoPath)
	if err != nil {
		return UploadResult{}, err
	}
	var thumbAbs string
	if in.ThumbnailPath != "" {
		thumbAbs, err = filepath.Abs(in.ThumbnailPath)
		if err != nil {
			return UploadResult{}, err
		}
	}

	var (
		currentURL  string
		channelName string
		videoID     string
	)

	// Step 1: navigate to YT Studio and check we're signed in.
	if err := chromedp.Run(bctx,
		chromedp.Navigate("https://studio.youtube.com"),
		chromedp.Sleep(3*time.Second),
		chromedp.Location(&currentURL),
	); err != nil {
		return UploadResult{}, fmt.Errorf("open studio: %w", err)
	}
	if detectSignIn(currentURL) {
		return UploadResult{}, ErrSessionExpired
	}

	// Read channel name; non-fatal if missing.
	_ = chromedp.Run(bctx, chromedp.Evaluate(jsReadChannelName, &channelName))
	channelName = strings.TrimSpace(channelName)
	d.logf("channel: %q", channelName)

	// Step 2: open the Create -> Upload video flow and attach the file.
	// YT Studio's upload uses a hidden file input. We click "Create" first
	// to open the menu, then look up the file input the dialog mounts.
	if err := chromedp.Run(bctx,
		clickByText("button", regexp.MustCompile(`(?i)create`)),
		chromedp.Sleep(500*time.Millisecond),
		// "Upload video" item — the menu has multiple entries.
		clickByText("*", regexp.MustCompile(`(?i)upload\s*video`)),
		chromedp.Sleep(1*time.Second),
		// Wait for the file input. YT Studio mounts <input type=file accept=video/*>
		// inside the upload dialog.
		waitForVisibleInput("input[type=file]"),
		chromedp.SetUploadFiles("input[type=file]", []string{abs}, chromedp.ByQuery),
	); err != nil {
		return UploadResult{}, fmt.Errorf("start upload: %w", err)
	}
	d.logf("file attached: %s", abs)

	// Step 3: details — title, description.
	if err := chromedp.Run(bctx,
		chromedp.WaitVisible("#title-textarea", chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		fillTextbox("#title-textarea #textbox", in.Title),
		fillTextbox("#description-textarea #textbox", in.Description),
	); err != nil {
		return UploadResult{}, fmt.Errorf("fill details: %w", err)
	}

	// Step 4: thumbnail (optional). The thumbnail tile has its own hidden
	// <input type=file accept=image/*>; finding it by accept attribute keeps
	// us off the main video input.
	if thumbAbs != "" {
		err := chromedp.Run(bctx,
			chromedp.SetUploadFiles(`ytcp-thumbnail-uploader input[type=file]`, []string{thumbAbs}, chromedp.ByQuery),
			chromedp.Sleep(1*time.Second),
		)
		if err != nil {
			d.logf("thumbnail upload failed: %v (continuing)", err)
		}
	}

	// Step 5: wait for "Checks complete". This is the slowest step; YT can
	// take many minutes on large files.
	checksCtx, cancelChecks := context.WithTimeout(bctx, DefaultChecksCompleteDeadline)
	defer cancelChecks()
	if err := chromedp.Run(checksCtx,
		waitForText(`(?i)checks complete`),
	); err != nil {
		return UploadResult{}, fmt.Errorf("wait checks complete: %w", err)
	}
	d.logf("checks complete")

	// Step 6: walk to the Visibility step. The dialog uses test-id buttons.
	if err := chromedp.Run(bctx,
		jsClick(`button[test-id='VIDEO_ELEMENTS']`),
		chromedp.Sleep(500*time.Millisecond),
		jsClick(`button[test-id='REVIEW']`),
		chromedp.Sleep(500*time.Millisecond),
		jsClick(`button[test-id='REVIEW']`), // some flows need it twice to advance
		chromedp.Sleep(500*time.Millisecond),
		chromedp.WaitVisible("tp-yt-paper-radio-button", chromedp.ByQuery),
		jsClick(fmt.Sprintf(`tp-yt-paper-radio-button[name='%s']`, visibility)),
		chromedp.Sleep(500*time.Millisecond),
	); err != nil {
		return UploadResult{}, fmt.Errorf("advance to visibility: %w", err)
	}

	// Step 7: capture the 11-char video ID from the dialog before saving.
	_ = chromedp.Run(bctx,
		chromedp.Evaluate(jsExtractVideoID, &videoID),
	)
	d.logf("captured video id (pre-save): %q", videoID)

	// Step 8: click Save.
	if err := chromedp.Run(bctx,
		jsClick(`ytcp-button[id='done-button'], button[aria-label='Save']:not([disabled])`),
		chromedp.Sleep(3*time.Second),
	); err != nil {
		return UploadResult{}, fmt.Errorf("save: %w", err)
	}

	// Fallback ID recovery if not captured pre-save.
	if videoID == "" {
		_ = chromedp.Run(bctx,
			chromedp.Sleep(2*time.Second),
			chromedp.Evaluate(jsExtractVideoIDFallback, &videoID),
		)
		d.logf("captured video id (fallback): %q", videoID)
	}

	if videoID == "" {
		return UploadResult{ChannelName: channelName}, errors.New("could not extract video id from YT Studio")
	}

	// Step 9: add to playlist (optional). Reopens the edit dialog.
	if in.PlaylistID != "" {
		if err := d.addToPlaylist(bctx, videoID, in.PlaylistID); err != nil {
			d.logf("add-to-playlist failed: %v", err)
			// Non-fatal — operator can fix manually.
		}
	}

	return UploadResult{VideoID: videoID, ChannelName: channelName}, nil
}

// addToPlaylist opens https://studio.youtube.com/video/<id>/edit and toggles
// the playlist checkbox identified by playlistID.
func (d *ChromedpDriver) addToPlaylist(ctx context.Context, videoID, playlistID string) error {
	editURL := fmt.Sprintf("https://studio.youtube.com/video/%s/edit", videoID)
	return chromedp.Run(ctx,
		chromedp.Navigate(editURL),
		chromedp.WaitVisible("#title-textarea", chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
		jsClick("ytcp-dropdown-trigger[use-placeholder]"),
		chromedp.Sleep(1*time.Second),
		jsClick(fmt.Sprintf(`[data-value='%s'], [id*='%s']`, playlistID, playlistID)),
		chromedp.Sleep(500*time.Millisecond),
		jsClick(`ytcp-button[test-id='done-button']`),
		chromedp.Sleep(1*time.Second),
		// Save the edit dialog to commit the playlist change.
		jsClick(`button[aria-label='Save']:not([disabled])`),
		chromedp.Sleep(2*time.Second),
	)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// clickByText finds the first element matching tagSelector whose textContent
// matches re, and clicks it. Implemented via Evaluate because chromedp's
// built-in selectors don't support text regex.
func clickByText(tagSelector string, re *regexp.Regexp) chromedp.Action {
	js := fmt.Sprintf(`
		(() => {
			const re = new RegExp(%q, 'i');
			const els = [...document.querySelectorAll(%q)];
			const el = els.find(e => re.test((e.textContent || '').trim()) && e.offsetParent !== null);
			if (el) { el.click(); return true; }
			return false;
		})()
	`, re.String(), tagSelector)
	var ok bool
	return chromedp.Evaluate(js, &ok)
}

// waitForVisibleInput polls until selector matches a visible element. The
// built-in WaitVisible returns when the element is in the DOM, which fires
// too early for some Studio panels that mount detached.
func waitForVisibleInput(selector string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			var nodes []*cdp.Node
			err := chromedp.Nodes(selector, &nodes, chromedp.ByQueryAll, chromedp.AtLeast(0)).Do(ctx)
			if err == nil && len(nodes) > 0 {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
		return fmt.Errorf("timeout waiting for %s", selector)
	})
}

// fillTextbox clears a contenteditable element and types fresh text. YT
// Studio's title and description fields are contenteditable, not <textarea>,
// so chromedp.SendKeys-on-the-element with a Ctrl-A first works most reliably.
func fillTextbox(selector, value string) chromedp.Action {
	js := fmt.Sprintf(`
		(() => {
			const el = document.querySelector(%q);
			if (!el) return false;
			el.focus();
			document.execCommand('selectAll', false, null);
			document.execCommand('insertText', false, %q);
			return true;
		})()
	`, selector, value)
	var ok bool
	return chromedp.Tasks{
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Evaluate(js, &ok),
		chromedp.Sleep(200 * time.Millisecond),
	}
}

// waitForText polls document.body.innerText against a regex.
func waitForText(pattern string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		for {
			var found bool
			js := fmt.Sprintf(`new RegExp(%q, 'i').test(document.body.innerText)`, pattern)
			if err := chromedp.Evaluate(js, &found).Do(ctx); err != nil {
				return err
			}
			if found {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
	})
}

// jsClick clicks the first element matching the CSS selector via JS, which
// bypasses pointer-event-blocking overlays that real-click intercepts.
func jsClick(selector string) chromedp.Action {
	js := fmt.Sprintf(`
		(() => {
			const el = document.querySelector(%q);
			if (el) { el.click(); return true; }
			return false;
		})()
	`, selector)
	var ok bool
	return chromedp.Evaluate(js, &ok)
}

// ─── JS snippets ──────────────────────────────────────────────────────────────

// jsReadChannelName returns "" when the channel name element is missing.
const jsReadChannelName = `
	(() => {
		const el = document.querySelector('#channel-name, .ytcp-channel-name, ytcp-entity-name');
		return el ? (el.innerText || el.textContent || '').trim() : '';
	})()
`

// jsExtractVideoID looks at the visible upload dialog for the YT URL link.
const jsExtractVideoID = `
	(() => {
		const links = [...document.querySelectorAll(
			"ytcp-video-info a[href*='youtu.be'], " +
			"ytcp-video-info a.video-url-fadeable, " +
			"a[href*='youtu.be/']"
		)];
		for (const l of links) {
			const m = (l.getAttribute('href') || '').match(/(?:youtu\.be\/|v=)([\w-]{11})/);
			if (m) return m[1];
		}
		return '';
	})()
`

// jsExtractVideoIDFallback runs after Save when the dialog is gone. The
// Studio "videos" page links each row to /video/<id>/edit.
const jsExtractVideoIDFallback = `
	(() => {
		const links = [...document.querySelectorAll("a[href*='studio.youtube.com/video/']")];
		for (const l of links) {
			const m = (l.getAttribute('href') || '').match(/\/video\/([\w-]{11})/);
			if (m) return m[1];
		}
		return '';
	})()
`
