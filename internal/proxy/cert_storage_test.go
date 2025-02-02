package proxy

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCertStorage(t *testing.T) {
	storage := NewCertStorage()
	assert.NotNil(t, storage)

	testCert1 := &tls.Certificate{
		Certificate: [][]byte{[]byte("test-cert-1")},
	}
	testCert2 := &tls.Certificate{
		Certificate: [][]byte{[]byte("test-cert-2")},
	}

	cert, err := storage.Fetch("example.com", func() (*tls.Certificate, error) {
		return testCert1, nil
	})
	assert.NoError(t, err)
	assert.Equal(t, testCert1, cert)

	cert2, err := storage.Fetch("example.com", func() (*tls.Certificate, error) {
		return testCert1, nil
	})
	assert.NoError(t, err)
	assert.Equal(t, cert, cert2)

	cert3, err := storage.Fetch("another-domain.com", func() (*tls.Certificate, error) {
		return testCert2, nil
	})
	assert.NoError(t, err)
	assert.NotEqual(t, cert, cert3)
}

func TestCertStorageConcurrency(t *testing.T) {
	storage := NewCertStorage()
	done := make(chan bool)
	testCert := &tls.Certificate{}

	for i := 0; i < 10; i++ {
		go func() {
			cert, err := storage.Fetch("example.com", func() (*tls.Certificate, error) {
				return testCert, nil
			})
			assert.NoError(t, err)
			assert.Equal(t, testCert, cert)
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
