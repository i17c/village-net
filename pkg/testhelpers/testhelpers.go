// Copyright 2016 CNI authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package testhelpers provides common support behavior for tests
package testhelpers

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	"golang.org/x/sys/unix"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func GetInode(path string) (uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	return GetInodeF(file)
}

func GetInodeF(file *os.File) (uint64, error) {
	stat := &unix.Stat_t{}
	err := unix.Fstat(int(file.Fd()), stat)
	return stat.Ino, err
}

func MakeNetworkNS(containerID string) string {
	namespace := "/var/run/netns/" + containerID

	err := os.MkdirAll("/var/run/netns", 0600)
	Expect(err).NotTo(HaveOccurred())

	// create an empty file at the mount point
	mountPointFd, err := os.Create(namespace)
	Expect(err).NotTo(HaveOccurred())
	mountPointFd.Close()

	var wg sync.WaitGroup
	wg.Add(1)

	// do namespace work in a dedicated goroutine, so that we can safely
	// Lock/Unlock OSThread without upsetting the lock/unlock state of
	// the caller of this function
	go (func() {
		defer wg.Done()

		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		defer GinkgoRecover()

		// capture current thread's original netns
		pid := unix.Getpid()
		tid := unix.Gettid()
		currentThreadNetNSPath := fmt.Sprintf("/proc/%d/task/%d/ns/net", pid, tid)
		originalNetNS, err := unix.Open(currentThreadNetNSPath, unix.O_RDONLY, 0)
		Expect(err).NotTo(HaveOccurred())
		defer unix.Close(originalNetNS)

		// create a new netns on the current thread
		err = unix.Unshare(unix.CLONE_NEWNET)
		Expect(err).NotTo(HaveOccurred())

		// bind mount the new netns from the current thread onto the mount point
		err = unix.Mount(currentThreadNetNSPath, namespace, "none", unix.MS_BIND, "")
		Expect(err).NotTo(HaveOccurred())

		// reset current thread's netns to the original
		_, _, e1 := unix.Syscall(unix.SYS_SETNS, uintptr(originalNetNS), uintptr(unix.CLONE_NEWNET), 0)
		Expect(e1).To(BeZero())
	})()

	wg.Wait()

	return namespace
}

func RemoveNetworkNS(networkNS string) error {
	err := unix.Unmount(networkNS, unix.MNT_DETACH)

	err = os.RemoveAll(networkNS)
	return err
}