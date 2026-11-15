package loadtest

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

// Hardware is the host specification written to hardware.json.
type Hardware struct {
	CPUModel       string `json:"cpu_model"`
	CPUCores       int    `json:"cpu_cores"`
	RAMBytes       uint64 `json:"ram_bytes"`
	DiskDevice     string `json:"disk_device"`
	DiskFilesystem string `json:"disk_filesystem"`
	DiskType       string `json:"disk_type"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	DockerVersion  string `json:"docker_version"`
	GoVersion      string `json:"go_version"`
	CollectedAt    int64  `json:"collected_at"`
}

// HostStat is one host sampling point.
type HostStat struct {
	CPUPercent         float64
	MemUsedMiB         float64
	MemPercent         float64
	DiskReadBytesPerS  float64
	DiskWriteBytesPerS float64
	Load1              float64
}

// hostSampler reads host CPU, memory and disk IO. gopsutil reports CPU and
// disk counters relative to their previous call, so the constructor primes
// both baselines before the first stage starts.
type hostSampler struct {
	prevRead  uint64
	prevWrite uint64
	prevTS    int64
}

// newHostSampler primes the process-wide CPU and disk baselines.
func newHostSampler(ctx context.Context) *hostSampler {
	// The first interval-0 CPU read only stores the baseline.
	_, _ = cpu.PercentWithContext(ctx, 0, false)

	s := &hostSampler{}
	counters, err := disk.IOCountersWithContext(ctx)
	if err == nil {
		s.prevRead, s.prevWrite = sumDiskIO(counters)
		s.prevTS = time.Now().Unix()
	}
	return s
}

// sample reads one host observation. CPU and memory are required; disk and
// load are best effort because some platforms do not support them.
func (s *hostSampler) sample(ctx context.Context) (HostStat, error) {
	var stat HostStat

	percent, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil {
		return HostStat{}, fmt.Errorf("host cpu: %w", err)
	}
	if len(percent) > 0 {
		stat.CPUPercent = percent[0]
	}

	vm, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return HostStat{}, fmt.Errorf("host memory: %w", err)
	}
	stat.MemUsedMiB = float64(vm.Used) / (1 << 20)
	stat.MemPercent = vm.UsedPercent

	if counters, err := disk.IOCountersWithContext(ctx); err == nil {
		read, write := sumDiskIO(counters)
		now := time.Now().Unix()
		if s.prevTS > 0 && now > s.prevTS {
			stat.DiskReadBytesPerS = float64(read-s.prevRead) / float64(now-s.prevTS)
			stat.DiskWriteBytesPerS = float64(write-s.prevWrite) / float64(now-s.prevTS)
		}
		s.prevRead, s.prevWrite, s.prevTS = read, write, now
	}

	if avg, err := load.AvgWithContext(ctx); err == nil {
		stat.Load1 = avg.Load1
	}
	return stat, nil
}

func sumDiskIO(counters map[string]disk.IOCountersStat) (read, write uint64) {
	for _, counter := range counters {
		read += counter.ReadBytes
		write += counter.WriteBytes
	}
	return read, write
}

// collectHardware gathers the host specification. Missing optional fields are
// left empty instead of failing the run.
func collectHardware(ctx context.Context) (Hardware, error) {
	hardware := Hardware{
		CPUModel:      "unknown",
		DiskType:      "unknown",
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		DockerVersion: "unknown",
		GoVersion:     runtime.Version(),
		CollectedAt:   time.Now().Unix(),
	}

	var errs []error

	if infos, err := cpu.InfoWithContext(ctx); err == nil && len(infos) > 0 {
		if infos[0].ModelName != "" {
			hardware.CPUModel = infos[0].ModelName
		}
	} else if err != nil {
		errs = append(errs, fmt.Errorf("cpu info: %w", err))
	}

	if cores, err := cpu.CountsWithContext(ctx, true); err == nil {
		hardware.CPUCores = cores
	} else {
		errs = append(errs, fmt.Errorf("cpu counts: %w", err))
	}

	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		hardware.RAMBytes = vm.Total
	} else {
		errs = append(errs, fmt.Errorf("memory info: %w", err))
	}

	if info, err := host.InfoWithContext(ctx); err == nil {
		hardware.OS = strings.TrimSpace(info.Platform + " " + info.PlatformVersion)
		if hardware.OS == "" {
			hardware.OS = info.OS
		}
		hardware.Arch = info.KernelArch
	} else {
		errs = append(errs, fmt.Errorf("host info: %w", err))
	}

	if partition, ok := rootPartition(ctx); ok {
		hardware.DiskDevice = partition.Device
		hardware.DiskFilesystem = partition.Fstype
		hardware.DiskType = diskType(ctx, partition.Mountpoint, partition.Device)
	} else {
		errs = append(errs, fmt.Errorf("root partition: %w", errNoRootPartition))
	}

	if version, err := dockerVersion(ctx); err == nil {
		hardware.DockerVersion = version
	} else {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return hardware, fmt.Errorf("collect hardware: %w", joinErrors(errs))
	}
	return hardware, nil
}

// rootPartition returns the filesystem mounted at / (or the first partition
// when the platform uses another root name).
func rootPartition(ctx context.Context) (disk.PartitionStat, bool) {
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil || len(partitions) == 0 {
		return disk.PartitionStat{}, false
	}
	for _, partition := range partitions {
		if partition.Mountpoint == "/" {
			return partition, true
		}
	}
	return partitions[0], true
}

// diskType reports SSD or HDD when the operating system exposes it and
// "unknown" otherwise.
func diskType(ctx context.Context, mountpoint, device string) string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.CommandContext(ctx, "diskutil", "info", mountpoint).Output() //nolint:gosec // mountpoint is the local root filesystem
		if err == nil {
			text := string(out)
			if strings.Contains(text, "Solid State: Yes") {
				return "SSD"
			}
			if strings.Contains(text, "Solid State: No") {
				return "HDD"
			}
		}
	case "linux":
		out, err := exec.CommandContext(ctx, "lsblk", "-dn", "-o", "ROTA", device).Output() //nolint:gosec // device is a local block device from gopsutil
		if err == nil {
			switch strings.TrimSpace(string(out)) {
			case "0":
				return "SSD"
			case "1":
				return "HDD"
			}
		}
	}
	return "unknown"
}

// dockerVersion returns the Docker server version.
func dockerVersion(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output() //nolint:gosec // the docker arguments are fixed strings
	if err != nil {
		return "", fmt.Errorf("docker version: %w", err)
	}
	version := strings.TrimSpace(string(out))
	if version == "" {
		return "", fmt.Errorf("docker version: empty output")
	}
	return version, nil
}

// errNoRootPartition is reported when gopsutil cannot list a root filesystem.
var errNoRootPartition = errors.New("no partition found")

// joinErrors joins the optional hardware probe failures into one line.
func joinErrors(errs []error) error {
	return errors.Join(errs...)
}
