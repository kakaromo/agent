package adb

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 60 * time.Second

// Device represents a single Android device connected via ADB.
type Device struct {
	Serial string
}

func NewDevice(serial string) *Device {
	return &Device{Serial: serial}
}

// Connect runs "adb connect <serial>".
func (d *Device) Connect(ctx context.Context) error {
	out, err := d.run(ctx, "connect", d.Serial)
	if err != nil {
		return fmt.Errorf("adb connect %s: %w", d.Serial, err)
	}
	if !strings.Contains(out, "connected") && !strings.Contains(out, "already") {
		return fmt.Errorf("adb connect %s: unexpected output: %s", d.Serial, out)
	}
	return nil
}

// Disconnect runs "adb disconnect <serial>".
func (d *Device) Disconnect(ctx context.Context) error {
	_, err := d.run(ctx, "disconnect", d.Serial)
	return err
}

// Shell runs a shell command on the device and returns stdout.
func (d *Device) Shell(ctx context.Context, cmd string) (string, error) {
	return d.run(ctx, "-s", d.Serial, "shell", cmd)
}

// Push pushes a local file to the device.
func (d *Device) Push(ctx context.Context, local, remote string) error {
	_, err := d.run(ctx, "-s", d.Serial, "push", local, remote)
	return err
}

// Pull pulls a remote file from the device.
func (d *Device) Pull(ctx context.Context, remote, local string) error {
	_, err := d.run(ctx, "-s", d.Serial, "pull", remote, local)
	return err
}

// IsAlive checks if the device is reachable.
func (d *Device) IsAlive(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := d.Shell(ctx, "echo ok")
	return err == nil && strings.TrimSpace(out) == "ok"
}

// GetProp reads an Android system property.
func (d *Device) GetProp(ctx context.Context, prop string) (string, error) {
	out, err := d.Shell(ctx, "getprop "+prop)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (d *Device) run(ctx context.Context, args ...string) (string, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, "adb", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
