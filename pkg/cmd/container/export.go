/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package container

import (
	"context"
	"fmt"
	"io"
	"os"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/pkg/archive"
	"github.com/containerd/log"

	"github.com/containerd/nerdctl/v2/pkg/api/types"
	"github.com/containerd/nerdctl/v2/pkg/idutil/containerwalker"
)

// Export exports a container's filesystem as a tar archive
func Export(ctx context.Context, client *containerd.Client, containerReq string, options types.ContainerExportOptions) error {
	walker := &containerwalker.ContainerWalker{
		Client: client,
		OnFound: func(ctx context.Context, found containerwalker.Found) error {
			if found.MatchCount > 1 {
				return fmt.Errorf("multiple IDs found with provided prefix: %s", found.Req)
			}
			return exportContainer(ctx, client, found.Container, options)
		},
	}

	n, err := walker.Walk(ctx, containerReq)
	if err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("no such container %s", containerReq)
	}
	return nil
}

func exportContainer(ctx context.Context, client *containerd.Client, container containerd.Container, options types.ContainerExportOptions) error {
	// Try to get a running container root first
	root, pid, err := getContainerRoot(ctx, container)
	var cleanup func() error

	if err != nil {
		// Container is not running, try to mount the snapshot
		var conInfo containers.Container
		conInfo, err = container.Info(ctx)
		if err != nil {
			return fmt.Errorf("failed to get container info: %w", err)
		}

		root, cleanup, err = MountSnapshotForContainer(ctx, client, conInfo, options.GOptions.Snapshotter)
		if cleanup != nil {
			defer func() {
				if cleanupErr := cleanup(); cleanupErr != nil {
					log.G(ctx).WithError(cleanupErr).Warn("Failed to cleanup mounted snapshot")
				}
			}()
		}

		if err != nil {
			return fmt.Errorf("failed to mount container snapshot: %w", err)
		}
		log.G(ctx).Debugf("Mounted snapshot at %s", root)
		// For stopped containers, set pid to 0 to avoid nsenter
		pid = 0
	} else {
		log.G(ctx).Debugf("Using running container root %s (pid %d)", root, pid)
	}

	// Create tar command to export the rootfs
	return createTarArchive(ctx, root, pid, options)
}

func getContainerRoot(ctx context.Context, container containerd.Container) (string, int, error) {
	task, err := container.Task(ctx, nil)
	if err != nil {
		return "", 0, err
	}

	status, err := task.Status(ctx)
	if err != nil {
		return "", 0, err
	}

	if status.Status != containerd.Running {
		return "", 0, fmt.Errorf("container is not running")
	}

	pid := int(task.Pid())
	return fmt.Sprintf("/proc/%d/root", pid), pid, nil
}

func createTarArchive(ctx context.Context, rootPath string, pid int, options types.ContainerExportOptions) error {
	// Create a temporary empty directory to use as the "before" state for WriteDiff
	emptyDir, err := os.MkdirTemp("", "nerdctl-export-empty-")
	if err != nil {
		return fmt.Errorf("failed to create temporary empty directory: %w", err)
	}
	defer os.RemoveAll(emptyDir)

	// Debug logging
	log.G(ctx).Debugf("Using WriteDiff to export container filesystem from %s", rootPath)
	log.G(ctx).Debugf("Empty directory: %s", emptyDir)
	log.G(ctx).Debugf("Output writer type: %T", options.Stdout)

	// Check if the rootPath directory exists and has contents
	if entries, err := os.ReadDir(rootPath); err != nil {
		log.G(ctx).Debugf("Failed to read rootPath directory %s: %v", rootPath, err)
	} else {
		log.G(ctx).Debugf("RootPath %s contains %d entries", rootPath, len(entries))
		for i, entry := range entries {
			if i < 10 { // Only log first 10 entries to avoid spam
				log.G(ctx).Debugf("  - %s (dir: %v)", entry.Name(), entry.IsDir())
			}
		}
		if len(entries) > 10 {
			log.G(ctx).Debugf("  ... and %d more entries", len(entries)-10)
		}
	}

	// Create a counting writer to track bytes written
	cw := &countingWriter{w: options.Stdout}

	// Use WriteDiff to create a tar stream comparing the container rootfs (rootPath)
	// with an empty directory (emptyDir). This produces a complete export of the container.
	err = archive.WriteDiff(ctx, cw, rootPath, emptyDir)
	if err != nil {
		return fmt.Errorf("failed to write tar diff: %w", err)
	}

	log.G(ctx).Debugf("WriteDiff completed successfully, wrote %d bytes", cw.count)

	return nil
}

// countingWriter wraps an io.Writer and counts the bytes written
type countingWriter struct {
	w     io.Writer
	count int64
}

func (cw *countingWriter) Write(p []byte) (n int, err error) {
	n, err = cw.w.Write(p)
	cw.count += int64(n)
	return n, err
}

func MountSnapshotForContainer(ctx context.Context, client *containerd.Client, conInfo containers.Container, snapshotter string) (string, func() error, error) {
	snapKey := conInfo.SnapshotKey
	resp, err := client.SnapshotService(snapshotter).Mounts(ctx, snapKey)
	if err != nil {
		return "", nil, err
	}

	tempDir, err := os.MkdirTemp("", "nerdctl-cp-")
	if err != nil {
		return "", nil, err
	}

	err = mount.All(resp, tempDir)
	if err != nil {
		return "", nil, err
	}

	cleanup := func() error {
		err = mount.Unmount(tempDir, 0)
		if err != nil {
			return err
		}
		return os.RemoveAll(tempDir)
	}

	return tempDir, cleanup, nil
}
