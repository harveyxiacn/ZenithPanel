package system

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ──────────────────────────────────────────────
// BBR Congestion Control
// ──────────────────────────────────────────────

type BBRStatus struct {
	Enabled   bool   `json:"enabled"`
	Current   string `json:"current"`
	Available string `json:"available"`
}

func GetBBRStatus() BBRStatus {
	status := BBRStatus{}

	// Read current congestion control
	if data, err := os.ReadFile("/proc/sys/net/ipv4/tcp_congestion_control"); err == nil {
		status.Current = strings.TrimSpace(string(data))
		status.Enabled = status.Current == "bbr"
	}

	// Read available congestion controls
	if data, err := os.ReadFile("/proc/sys/net/ipv4/tcp_available_congestion_control"); err == nil {
		status.Available = strings.TrimSpace(string(data))
	}

	return status
}

const bbrSysctlFile = "/etc/sysctl.d/99-zenith-bbr.conf"

func EnableBBR() error {
	// Check if BBR module is available
	avail, err := os.ReadFile("/proc/sys/net/ipv4/tcp_available_congestion_control")
	if err != nil {
		return fmt.Errorf("cannot read available congestion controls: %w", err)
	}
	if !strings.Contains(string(avail), "bbr") {
		// Try loading the module
		exec.Command("modprobe", "tcp_bbr").Run()
		// Re-check
		avail, _ = os.ReadFile("/proc/sys/net/ipv4/tcp_available_congestion_control")
		if !strings.Contains(string(avail), "bbr") {
			return fmt.Errorf("BBR is not available on this kernel. Kernel 4.9+ required")
		}
	}

	content := "# ZenithPanel BBR optimization\nnet.core.default_qdisc=fq\nnet.ipv4.tcp_congestion_control=bbr\n"
	if err := os.MkdirAll(filepath.Dir(bbrSysctlFile), 0755); err != nil {
		return fmt.Errorf("failed to create sysctl.d directory: %w", err)
	}
	if err := os.WriteFile(bbrSysctlFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write sysctl config: %w", err)
	}

	out, err := exec.Command("sysctl", "-p", bbrSysctlFile).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sysctl apply failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func DisableBBR() error {
	os.Remove(bbrSysctlFile)

	// Revert to cubic (Linux default)
	exec.Command("sysctl", "-w", "net.core.default_qdisc=fq_codel").Run()
	exec.Command("sysctl", "-w", "net.ipv4.tcp_congestion_control=cubic").Run()
	return nil
}

// ──────────────────────────────────────────────
// Swap Management
// ──────────────────────────────────────────────

type SwapStatus struct {
	Enabled  bool   `json:"enabled"`
	TotalMB  int64  `json:"total_mb"`
	UsedMB   int64  `json:"used_mb"`
	FilePath string `json:"file_path"`
}

const defaultSwapFile = "/swapfile"

func GetSwapStatus() SwapStatus {
	status := SwapStatus{}

	out, err := exec.Command("swapon", "--show=NAME,SIZE,USED", "--noheadings", "--bytes").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return status
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			status.Enabled = true
			status.FilePath = fields[0]
			if size, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
				status.TotalMB += size / (1024 * 1024)
			}
			if used, err := strconv.ParseInt(fields[2], 10, 64); err == nil {
				status.UsedMB += used / (1024 * 1024)
			}
		}
	}
	return status
}

func CreateSwap(sizeMB int) error {
	if sizeMB < 256 || sizeMB > 16384 {
		return fmt.Errorf("swap size must be between 256 MB and 16384 MB")
	}

	// Check if swap already exists
	if _, err := os.Stat(defaultSwapFile); err == nil {
		return fmt.Errorf("swap file already exists at %s. Remove it first", defaultSwapFile)
	}

	// Create swap file using dd (more compatible than fallocate)
	count := strconv.Itoa(sizeMB)
	out, err := exec.Command("dd", "if=/dev/zero", "of="+defaultSwapFile, "bs=1M", "count="+count, "status=none").CombinedOutput()
	if err != nil {
		os.Remove(defaultSwapFile)
		return fmt.Errorf("failed to create swap file: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	// Set permissions
	if err := os.Chmod(defaultSwapFile, 0600); err != nil {
		os.Remove(defaultSwapFile)
		return fmt.Errorf("failed to set swap file permissions: %w", err)
	}

	// Format as swap
	out, err = exec.Command("mkswap", defaultSwapFile).CombinedOutput()
	if err != nil {
		os.Remove(defaultSwapFile)
		return fmt.Errorf("mkswap failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	// Enable
	out, err = exec.Command("swapon", defaultSwapFile).CombinedOutput()
	if err != nil {
		os.Remove(defaultSwapFile)
		return fmt.Errorf("swapon failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}

	// Add to fstab if not already there
	fstab, _ := os.ReadFile("/etc/fstab")
	if !strings.Contains(string(fstab), defaultSwapFile) {
		entry := fmt.Sprintf("\n%s none swap sw 0 0\n", defaultSwapFile)
		f, err := os.OpenFile("/etc/fstab", os.O_APPEND|os.O_WRONLY, 0644)
		if err == nil {
			f.WriteString(entry)
			f.Close()
		}
	}

	return nil
}

func RemoveSwap() error {
	// Disable swap
	exec.Command("swapoff", defaultSwapFile).Run()

	// Remove file
	os.Remove(defaultSwapFile)

	// Remove from fstab
	fstab, err := os.ReadFile("/etc/fstab")
	if err == nil {
		lines := strings.Split(string(fstab), "\n")
		var newLines []string
		for _, line := range lines {
			if !strings.Contains(line, defaultSwapFile) {
				newLines = append(newLines, line)
			}
		}
		os.WriteFile("/etc/fstab", []byte(strings.Join(newLines, "\n")), 0644)
	}

	return nil
}

// ──────────────────────────────────────────────
// Sysctl Network Tuning
// ──────────────────────────────────────────────

type SysctlStatus struct {
	Enabled bool              `json:"enabled"`
	Params  map[string]string `json:"params"`
}

const sysctlTuningFile = "/etc/sysctl.d/99-zenith-network.conf"

var networkTuningParams = map[string]string{
	"net.core.rmem_max":                 "16777216",
	"net.core.wmem_max":                 "16777216",
	"net.ipv4.tcp_rmem":                 "4096 87380 16777216",
	"net.ipv4.tcp_wmem":                 "4096 65536 16777216",
	"net.ipv4.tcp_fastopen":             "3",
	"net.ipv4.tcp_slow_start_after_idle": "0",
	"net.ipv4.tcp_mtu_probing":          "1",
	"net.core.somaxconn":                "65535",
	"net.core.netdev_max_backlog":       "65535",
	"net.ipv4.tcp_max_syn_backlog":      "65535",
	"net.ipv4.tcp_tw_reuse":             "1",
	"net.ipv4.ip_local_port_range":      "1024 65535",
	"fs.file-max":                       "1048576",
}

func GetSysctlTuningStatus() SysctlStatus {
	status := SysctlStatus{Params: make(map[string]string)}

	_, err := os.Stat(sysctlTuningFile)
	status.Enabled = err == nil

	// Read current values
	for key := range networkTuningParams {
		path := "/proc/sys/" + strings.ReplaceAll(strings.ReplaceAll(key, ".", "/"), "-", "_")
		if data, err := os.ReadFile(path); err == nil {
			status.Params[key] = strings.TrimSpace(string(data))
		}
	}

	return status
}

func EnableSysctlTuning() error {
	var lines []string
	lines = append(lines, "# ZenithPanel network optimization")
	for key, val := range networkTuningParams {
		lines = append(lines, fmt.Sprintf("%s=%s", key, val))
	}

	if err := os.MkdirAll(filepath.Dir(sysctlTuningFile), 0755); err != nil {
		return fmt.Errorf("failed to create sysctl.d directory: %w", err)
	}
	if err := os.WriteFile(sysctlTuningFile, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		return fmt.Errorf("failed to write sysctl config: %w", err)
	}

	out, err := exec.Command("sysctl", "-p", sysctlTuningFile).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sysctl apply failed: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func DisableSysctlTuning() error {
	os.Remove(sysctlTuningFile)

	// Reload default sysctl
	exec.Command("sysctl", "--system").Run()
	return nil
}

// ──────────────────────────────────────────────
// System Cleanup
// ──────────────────────────────────────────────

type CleanupInfo struct {
	JournalSize string `json:"journal_size"`
	PackageSize string `json:"package_size"`
	DockerSize  string `json:"docker_size"`
	TotalSize   string `json:"total_size"`
}

type CleanupResult struct {
	JournalFreed string `json:"journal_freed"`
	PackageFreed string `json:"package_freed"`
	DockerFreed  string `json:"docker_freed"`
	Success      bool   `json:"success"`
}

func GetCleanupInfo() CleanupInfo {
	info := CleanupInfo{}

	// Journal log size
	out, err := exec.Command("journalctl", "--disk-usage").CombinedOutput()
	if err == nil {
		s := string(out)
		// Format: "Archived and active journals take up 123.4M in the file system."
		if idx := strings.Index(s, "take up "); idx >= 0 {
			rest := s[idx+8:]
			if end := strings.Index(rest, " "); end >= 0 {
				// Include unit (next field might be part of size like "1.2G")
				// Actually journalctl outputs like "take up 56.0M in the..."
				info.JournalSize = strings.TrimSpace(rest[:end])
			}
		}
	}
	if info.JournalSize == "" {
		info.JournalSize = "N/A"
	}

	// Package cache size (apt)
	out, err = exec.Command("du", "-sh", "/var/cache/apt/archives").CombinedOutput()
	if err == nil {
		fields := strings.Fields(string(out))
		if len(fields) > 0 {
			info.PackageSize = fields[0]
		}
	}
	if info.PackageSize == "" {
		// Try yum/dnf cache
		out, err = exec.Command("du", "-sh", "/var/cache/yum").CombinedOutput()
		if err == nil {
			fields := strings.Fields(string(out))
			if len(fields) > 0 {
				info.PackageSize = fields[0]
			}
		}
	}
	if info.PackageSize == "" {
		info.PackageSize = "N/A"
	}

	// Docker reclaimable space
	out, err = exec.Command("docker", "system", "df", "--format", "{{.Reclaimable}}").CombinedOutput()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 {
			info.DockerSize = strings.Join(lines, " / ")
		}
	}
	if info.DockerSize == "" {
		info.DockerSize = "N/A"
	}

	return info
}

func RunCleanup() CleanupResult {
	result := CleanupResult{Success: true}

	// Clean journal logs (keep 3 days)
	out, _ := exec.Command("journalctl", "--vacuum-time=3d").CombinedOutput()
	if idx := strings.Index(string(out), "freed"); idx >= 0 {
		// Extract size info before "freed"
		s := string(out)[:idx]
		lines := strings.Split(s, "\n")
		if len(lines) > 0 {
			last := strings.TrimSpace(lines[len(lines)-1])
			// Find the size value
			fields := strings.Fields(last)
			if len(fields) >= 1 {
				result.JournalFreed = fields[len(fields)-1]
			}
		}
	}
	if result.JournalFreed == "" {
		result.JournalFreed = "0B"
	}

	// Clean package cache
	// Try apt first
	if _, err := exec.LookPath("apt-get"); err == nil {
		exec.Command("apt-get", "clean").Run()
		exec.Command("apt-get", "autoremove", "-y").Run()
		result.PackageFreed = "cleaned"
	} else if _, err := exec.LookPath("yum"); err == nil {
		exec.Command("yum", "clean", "all").Run()
		result.PackageFreed = "cleaned"
	} else if _, err := exec.LookPath("dnf"); err == nil {
		exec.Command("dnf", "clean", "all").Run()
		result.PackageFreed = "cleaned"
	} else {
		result.PackageFreed = "N/A"
	}

	// Docker prune (non-interactive)
	out, err := exec.Command("docker", "system", "prune", "-f").CombinedOutput()
	if err == nil {
		s := string(out)
		if idx := strings.Index(s, "Total reclaimed space:"); idx >= 0 {
			result.DockerFreed = strings.TrimSpace(s[idx+len("Total reclaimed space:"):])
		}
	}
	if result.DockerFreed == "" {
		result.DockerFreed = "N/A"
	}

	return result
}
