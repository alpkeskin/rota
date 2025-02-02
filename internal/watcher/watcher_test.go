package watcher

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockProxyLoader struct {
	mock.Mock
}

func (m *MockProxyLoader) Reload() error {
	args := m.Called()
	return args.Error(0)
}

func TestNewFileWatcher(t *testing.T) {
	mockLoader := &MockProxyLoader{}

	t.Run("successful watcher creation", func(t *testing.T) {
		fw, err := NewFileWatcher(mockLoader)
		assert.NoError(t, err)
		assert.NotNil(t, fw)
		fw.Close()
	})
}

func TestFileWatcher_Watch(t *testing.T) {
	mockLoader := &MockProxyLoader{}

	t.Run("invalid file path", func(t *testing.T) {
		fw, _ := NewFileWatcher(mockLoader)
		defer fw.Close()

		err := fw.Watch("/invalid/file/path")
		assert.Error(t, err)
	})

	t.Run("file change monitoring", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "proxy-*.txt")
		assert.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		mockLoader.On("Reload").Return(nil)

		fw, _ := NewFileWatcher(mockLoader)
		defer fw.Close()

		err = fw.Watch(tmpFile.Name())
		assert.NoError(t, err)

		time.Sleep(100 * time.Millisecond)
		err = os.WriteFile(tmpFile.Name(), []byte("test"), 0644)
		assert.NoError(t, err)

		time.Sleep(100 * time.Millisecond)
		mockLoader.AssertExpectations(t)
	})

	t.Run("reload error", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "proxy-*.txt")
		assert.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		mockLoader.On("Reload").Return(errors.New("reload error"))

		fw, _ := NewFileWatcher(mockLoader)
		defer fw.Close()

		err = fw.Watch(tmpFile.Name())
		assert.NoError(t, err)

		time.Sleep(100 * time.Millisecond)
		err = os.WriteFile(tmpFile.Name(), []byte("test"), 0644)
		assert.NoError(t, err)

		time.Sleep(100 * time.Millisecond)
		mockLoader.AssertExpectations(t)
	})
}

func TestFileWatcher_Close(t *testing.T) {
	mockLoader := &MockProxyLoader{}

	fw, _ := NewFileWatcher(mockLoader)
	err := fw.Close()
	assert.NoError(t, err)
}
