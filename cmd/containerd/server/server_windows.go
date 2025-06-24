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

package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"syscall"
	"unsafe"

	srvconfig "github.com/containerd/containerd/v2/cmd/containerd/server/config"
	"github.com/containerd/log"
	"github.com/containerd/otelttrpc"
	"github.com/containerd/ttrpc"
	"github.com/google/certtostore"
	"golang.org/x/sys/windows"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func apply(_ context.Context, _ *srvconfig.Config) error {
	return nil
}

func newTTRPCServer() (*ttrpc.Server, error) {
	return ttrpc.NewServer(
		ttrpc.WithUnaryServerInterceptor(otelttrpc.UnaryServerInterceptor()),
	)
}

// setupTLSFromWindowsCertStore sets up TLS configuration using certificates from Windows Certificate Store.
func setupTLSFromWindowsCertStore(ctx context.Context, config *srvconfig.Config) ([]grpc.ServerOption, error) {
	var tcpServerOpts []grpc.ServerOption

	log.G(ctx).Infof("Setting up TLS on TCP gRPC services with common name %v", config.GRPC.TCPTLSCName)

	storeName, err := syscall.UTF16PtrFromString("My")
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to convert store name to UTF16")
		return nil, fmt.Errorf("failed to convert store name to UTF16: %w", err)
	}

	winStore, err := windows.CertOpenStore(windows.CERT_STORE_PROV_SYSTEM,
		windows.X509_ASN_ENCODING|windows.PKCS_7_ASN_ENCODING, 0,
		windows.CERT_SYSTEM_STORE_LOCAL_MACHINE, uintptr(unsafe.Pointer(storeName)))
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to open certificate store")
		return nil, fmt.Errorf("failed to open windows certificate store: %w", err)
	}
	defer windows.CertCloseStore(winStore, 0)

	commonName, err := syscall.UTF16PtrFromString(config.GRPC.TCPTLSCName)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to convert common name to UTF16")
		return nil, fmt.Errorf("failed to convert common name to UTF16: %w", err)
	}

	// Find the certificate by common name in the Windows Certificate Store.
	certContext, err := windows.CertFindCertificateInStore(winStore,
		windows.X509_ASN_ENCODING|windows.PKCS_7_ASN_ENCODING,
		0, windows.CERT_FIND_SUBJECT_STR, unsafe.Pointer(commonName), nil)
	if err != nil || certContext == nil {
		log.G(ctx).WithError(err).Errorf("failed to find certificate in store")
		return nil, fmt.Errorf("failed to find certificate in store: %w", err)
	}
	defer windows.CertFreeCertificateContext(certContext)

	// Parse the leaf certificate from certContext
	certDER := unsafe.Slice(certContext.EncodedCert, certContext.Length)
	leafCert, err := x509.ParseCertificate(certDER)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to parse leaf certificate")
		return nil, fmt.Errorf("failed to parse leaf certificate: %w", err)
	}

	// Retrieve the certificate chain
	var certChain *windows.CertChainContext
	var chainPara windows.CertChainPara
	chainPara.Size = uint32(unsafe.Sizeof(chainPara))
	err = windows.CertGetCertificateChain(0, certContext, nil, 0, &chainPara, 0, 0, &certChain)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to retrieve certificate chain")
		return nil, fmt.Errorf("failed to retrieve certificate chain: %w", err)
	}
	defer windows.CertFreeCertificateChain(certChain)

	// Convert the certificate chain to a Go x509.CertPool and create certificate pool
	certPool := x509.NewCertPool()
	chains := unsafe.Slice(certChain.Chains, certChain.ChainCount)
	var certChainBytes [][]byte
	for i := 0; i < int(certChain.ChainCount); i++ {
		elements := unsafe.Slice(chains[i].Elements, chains[i].NumElements)
		for j := 0; j < int(chains[i].NumElements); j++ {
			if i == 0 && j == 0 {
				continue // Skip leaf certificate
			}
			lcertContext := elements[j].CertContext
			certBytes := make([]byte, lcertContext.Length)
			copy(certBytes, unsafe.Slice((*byte)(unsafe.Pointer(lcertContext.EncodedCert)), lcertContext.Length))
			certChainBytes = append(certChainBytes, certBytes)
			cert, err := x509.ParseCertificate(certBytes)
			if err != nil {
				log.G(ctx).WithError(err).Errorf("failed to parse certificate from chain")
				return nil, fmt.Errorf("failed to parse certificate from chain: %v", err)
			}
			certPool.AddCert(cert)
		}
	}

	// Open the Windows Certificate Store to retrieve the private key. certtostore implements crypto.Signer
	// and crypto.Decrypter interfaces for private key operations.
	store, err := certtostore.OpenWinCertStore(certtostore.ProviderMSSoftware, "",
		[]string{leafCert.Issuer.CommonName}, nil, false)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to open Windows Certificate Store")
		return nil, fmt.Errorf("failed to open Windows Certificate Store: %w", err)
	}
	defer store.Close()

	key, err := store.CertKey(certContext)
	if err != nil {
		log.G(ctx).WithError(err).Errorf("failed to retrieve private key")
		return nil, fmt.Errorf("failed to retrieve private key: %w", err)
	}

	tlsCert := tls.Certificate{
		PrivateKey:  key,
		Leaf:        leafCert,
		Certificate: append([][]byte{leafCert.Raw}, certChainBytes...),
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		ClientCAs:    certPool,
	}

	if len(certChainBytes) > 0 {
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	tcpServerOpts = append(tcpServerOpts, grpc.Creds(credentials.NewTLS(tlsConfig)))

	log.G(ctx).Infof("Loaded TLS configuration successfully")

	return tcpServerOpts, nil
}
