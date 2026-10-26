package cloud

import (
	"os"
	"os/exec"
	"time"
)

// maxLog is where the background log starts over.
const maxLog = 1 << 20

// Spawner starts `clai cloud sync -background` detached, its output
// appended to logPath.
type Spawner func(logPath string) error

// SpawnSelf re-executes this binary.
func SpawnSelf(logPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLog {
		_ = os.Truncate(logPath, 0)
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	c := exec.Command(exe, "cloud", "sync", "-background")
	c.Stdout, c.Stderr = log, log
	c.Env = append(os.Environ(), "NO_COLOR=1")
	detach(c)
	if err := c.Start(); err != nil {
		return err
	}
	return c.Process.Release()
}

// skipAuto are the commands after which no sync starts: the cloud command
// itself, and commands that touch nothing a store holds.
var skipAuto = map[string]bool{
	"cloud": true, "version": true, "--version": true, "-version": true, "confdir": true,
	"help": true, "h": true, "-h": true, "--help": true, "completion": true, "__complete": true,
}

// AutoSync starts a background sync after a command when this machine is
// logged in, CLAI_CLOUD is not off, and the last sync is older than the
// settings' interval. It never blocks the command and never fails it.
func AutoSync(confDir, cacheDir string, args []string, now time.Time, spawn Spawner) bool {
	if len(args) == 0 || skipAuto[args[0]] || Off() {
		return false
	}
	if _, err := LoadCredentials(confDir); err != nil {
		return false
	}
	settings, err := LoadSettings(confDir)
	if err != nil {
		return false
	}
	enabled := false
	for _, s := range Stores {
		enabled = enabled || settings.Enabled(s)
	}
	if !enabled {
		return false
	}
	last := LastSync(cacheDir)
	if !last.IsZero() && now.Sub(last) < time.Duration(settings.MinIntervalSeconds)*time.Second {
		return false
	}
	touch(lastPath(cacheDir), now)
	if err := os.MkdirAll(runDir(cacheDir), 0o700); err != nil {
		return false
	}
	return spawn(LogPath(cacheDir)) == nil
}
