// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fuzz

import (
	"fmt"
	"io/ioutil"
	"os"
	"unsafe"
)

// sharedMem manages access to a region of virtual memory mapped from a file,
// shared between multiple processes. The region includes space for a header and
// a value of variable length.
//
// When fuzzing, the coordinator creates a sharedMem from a temporary file for
// each worker. This buffer is used to pass values to fuzz between processes.
//
// Care must be taken to synchronize access to shared memory across processes.
// For example, workerClient and workerServer use an RPC protocol over pipes:
// workerServer may access shared memory when handling an RPC; workerClient may
// access shared memory at other times.
type sharedMem struct {
	// f is the file mapped into memory.
	f *os.File

	// region is the mapped region of virtual memory for f. The content of f may
	// be read or written through this slice.
	region []byte

	// removeOnClose is true if the file should be deleted by Close.
	removeOnClose bool

	// sys contains OS-specific information.
	sys sharedMemSys
}

// sharedMemHeader stores metadata in shared memory.
type sharedMemHeader struct {
	length int
}

// sharedMemSize returns the size needed for a shared memory buffer that can
// contain values of the given size.
func sharedMemSize(valueSize int) int {
	// TODO(jayconrod): set a reasonable maximum size per platform.
	return int(unsafe.Sizeof(sharedMemHeader{})) + valueSize
}

// sharedMemTempFile creates a new temporary file large enough to hold a value
// of the given size, then maps it into memory. The file will be removed when
// the Close method is called.
func sharedMemTempFile(valueSize int) (m *sharedMem, err error) {
	// Create a temporary file.
	f, err := ioutil.TempFile("", "fuzz-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()

	// Resize it to the correct size.
	totalSize := sharedMemSize(valueSize)
	if err := f.Truncate(int64(totalSize)); err != nil {
		return nil, err
	}

	// Map the file into memory.
	removeOnClose := true
	return sharedMemMapFile(f, totalSize, removeOnClose)
}

// header returns a pointer to metadata within the shared memory region.
func (m *sharedMem) header() *sharedMemHeader {
	return (*sharedMemHeader)(unsafe.Pointer(&m.region[0]))
}

// value returns the value currently stored in shared memory. The returned slice
// points to shared memory; it is not a copy.
func (m *sharedMem) value() []byte {
	length := m.header().length
	valueOffset := int(unsafe.Sizeof(sharedMemHeader{}))
	return m.region[valueOffset : valueOffset+length]
}

// setValue copies the data in b into the shared memory buffer and sets
// the length. len(b) must be less than or equal to the capacity of the buffer
// (as returned by cap(m.value())).
func (m *sharedMem) setValue(b []byte) {
	v := m.value()
	if len(b) > cap(v) {
		panic(fmt.Sprintf("value length %d larger than shared memory capacity %d", len(b), cap(v)))
	}
	m.header().length = len(b)
	copy(v[:cap(v)], b)
}

// TODO(jayconrod): add method to resize the buffer. We'll need that when the
// mutator can increase input length. Only the coordinator will be able to
// do it, since we'll need to send a message to the worker telling it to
// remap the file.
