package monitor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"agent/adb"
	pb "agent/pb"
)

const separator = "---SEP---"

// Collector gathers device metrics via ADB shell commands.
type Collector struct {
	manager *adb.Manager
}

func NewCollector(manager *adb.Manager) *Collector {
	return &Collector{manager: manager}
}

// CollectMetrics collects a single snapshot of metrics from a device.
func (c *Collector) CollectMetrics(ctx context.Context, deviceID string, prevCPU *cpuSample) (*pb.DeviceMetrics, *cpuSample, error) {
	md, err := c.manager.GetDevice(deviceID)
	if err != nil {
		return nil, prevCPU, err
	}

	cmd := fmt.Sprintf(
		"cat /proc/stat; echo '%s'; cat /proc/meminfo; echo '%s'; cat /proc/diskstats; echo '%s'; df /data; echo '%s'; mount | grep ' /data '",
		separator, separator, separator, separator,
	)

	out, err := md.Device.Shell(ctx, cmd)
	if err != nil {
		return nil, prevCPU, fmt.Errorf("shell command failed: %w", err)
	}

	parts := strings.Split(out, separator+"\n")
	if len(parts) < 5 {
		// try with \r\n
		parts = strings.Split(out, separator+"\r\n")
	}
	if len(parts) < 5 {
		return nil, prevCPU, fmt.Errorf("unexpected output format: got %d parts", len(parts))
	}

	metrics := &pb.DeviceMetrics{
		DeviceId:  deviceID,
		Timestamp: time.Now().UnixMilli(),
	}

	// CPU
	curCPU := parseProcStat(parts[0])
	if prevCPU != nil {
		metrics.Cpu = computeCPUMetrics(prevCPU, curCPU)
	} else {
		metrics.Cpu = &pb.CpuMetrics{}
	}

	// Memory
	metrics.Memory = parseMeminfo(parts[1])

	// Disk
	metrics.Disk = parseDiskStats(parts[2])

	// /data partition
	metrics.DataPartition = parseDfAndMount(parts[3], parts[4])

	return metrics, curCPU, nil
}

// StreamMetrics streams metrics at the given interval until context is cancelled.
func (c *Collector) StreamMetrics(ctx context.Context, deviceID string, intervalSec uint32, ch chan<- *pb.DeviceMetrics) {
	if intervalSec == 0 {
		intervalSec = 5
	}

	var prevCPU *cpuSample
	ticker := time.NewTicker(time.Duration(intervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			metrics, newCPU, err := c.CollectMetrics(ctx, deviceID, prevCPU)
			if err != nil {
				continue
			}
			prevCPU = newCPU
			select {
			case ch <- metrics:
			case <-ctx.Done():
				return
			}
		}
	}
}

// ==================== Parsers ====================

type cpuSample struct {
	total   []uint64 // per-field values for "cpu" line
	perCore [][]uint64
}

func parseProcStat(data string) *cpuSample {
	sample := &cpuSample{}
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "cpu ") {
			sample.total = parseCPUFields(line)
		} else if strings.HasPrefix(line, "cpu") {
			sample.perCore = append(sample.perCore, parseCPUFields(line))
		}
	}
	return sample
}

func parseCPUFields(line string) []uint64 {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return nil
	}
	vals := make([]uint64, 0, len(fields)-1)
	for _, f := range fields[1:] {
		v, _ := strconv.ParseUint(f, 10, 64)
		vals = append(vals, v)
	}
	return vals
}

func computeCPUMetrics(prev, cur *cpuSample) *pb.CpuMetrics {
	m := &pb.CpuMetrics{}
	m.UsagePercent = calcUsage(prev.total, cur.total)
	minCores := len(prev.perCore)
	if len(cur.perCore) < minCores {
		minCores = len(cur.perCore)
	}
	for i := 0; i < minCores; i++ {
		m.PerCorePercent = append(m.PerCorePercent, calcUsage(prev.perCore[i], cur.perCore[i]))
	}
	return m
}

func calcUsage(prev, cur []uint64) float64 {
	if len(prev) < 4 || len(cur) < 4 {
		return 0
	}
	var prevTotal, curTotal uint64
	for _, v := range prev {
		prevTotal += v
	}
	for _, v := range cur {
		curTotal += v
	}
	totalDelta := float64(curTotal - prevTotal)
	if totalDelta == 0 {
		return 0
	}
	// idle is field index 3
	idleDelta := float64(cur[3] - prev[3])
	return (1 - idleDelta/totalDelta) * 100
}

func parseMeminfo(data string) *pb.MemoryMetrics {
	m := &pb.MemoryMetrics{}
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "MemTotal:") {
			m.TotalKb = parseMemValue(line)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			m.AvailableKb = parseMemValue(line)
		}
	}
	if m.TotalKb > 0 {
		m.UsedKb = m.TotalKb - m.AvailableKb
		m.UsagePercent = float64(m.UsedKb) / float64(m.TotalKb) * 100
	}
	return m
}

func parseMemValue(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	v, _ := strconv.ParseUint(fields[1], 10, 64)
	return v
}

func parseDiskStats(data string) *pb.DiskMetrics {
	m := &pb.DiskMetrics{}
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 14 {
			continue
		}
		// Look for common block devices
		devName := fields[2]
		if devName == "sda" || devName == "dm-0" || devName == "mmcblk0" || devName == "vda" {
			readIOs, _ := strconv.ParseUint(fields[3], 10, 64)
			readSectors, _ := strconv.ParseUint(fields[5], 10, 64)
			writeIOs, _ := strconv.ParseUint(fields[7], 10, 64)
			writeSectors, _ := strconv.ParseUint(fields[9], 10, 64)
			m.ReadIos += readIOs
			m.ReadBytes += readSectors * 512
			m.WriteIos += writeIOs
			m.WriteBytes += writeSectors * 512
		}
	}
	return m
}

func parseDfAndMount(dfData, mountData string) *pb.FilesystemInfo {
	fi := &pb.FilesystemInfo{
		MountPoint: "/data",
	}

	// Parse df output
	for _, line := range strings.Split(strings.TrimSpace(dfData), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		// Skip header
		if fields[0] == "Filesystem" {
			continue
		}
		// Match /data or /data/* mount point (last field)
		lastField := fields[len(fields)-1]
		if lastField == "/data" || strings.HasPrefix(lastField, "/data/") {
			// df output: Filesystem 1K-blocks Used Available Use% Mounted
			if len(fields) >= 6 {
				total, _ := strconv.ParseUint(fields[1], 10, 64)
				used, _ := strconv.ParseUint(fields[2], 10, 64)
				avail, _ := strconv.ParseUint(fields[3], 10, 64)
				fi.TotalBytes = total * 1024
				fi.UsedBytes = used * 1024
				fi.AvailableBytes = avail * 1024
				if fi.TotalBytes > 0 {
					fi.UsagePercent = float64(fi.UsedBytes) / float64(fi.TotalBytes) * 100
				}
			}
			break
		}
	}

	// Parse mount output for filesystem type
	for _, line := range strings.Split(strings.TrimSpace(mountData), "\n") {
		// format: /dev/xxx on /data type ext4 (rw,...)
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "type" && i+1 < len(fields) {
				fi.Filesystem = fields[i+1]
				break
			}
		}
	}

	return fi
}
