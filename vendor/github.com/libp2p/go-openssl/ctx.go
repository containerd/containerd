// Copyright (C) 2017. See AUTHORS.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package openssl

// #include "shim.h"
import "C"

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/spacemonkeygo/spacelog"
)

var (
	ssl_ctx_idx = C.X_SSL_CTX_new_index()

	logger = spacelog.GetLogger()
)

type Ctx struct {
	ctx       *C.SSL_CTX
	cert      *Certificate
	chain     []*Certificate
	key       PrivateKey
	verify_cb VerifyCallback
	sni_cb    TLSExtServernameCallback

	ticket_store_mu sync.Mutex
	ticket_store    *TicketStore
}

//export get_ssl_ctx_idx
func get_ssl_ctx_idx() C.int {
	return ssl_ctx_idx
}

func newCtx(method *C.SSL_METHOD) (*Ctx, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ctx := C.SSL_CTX_new(method)
	if ctx == nil {
		return nil, errorFromErrorQueue()
	}
	c := &Ctx{ctx: ctx}
	C.SSL_CTX_set_ex_data(ctx, get_ssl_ctx_idx(), unsafe.Pointer(c))
	runtime.SetFinalizer(c, func(c *Ctx) {
		C.SSL_CTX_free(c.ctx)
	})
	return c, nil
}

type SSLVersion int

const (
	SSLv3   SSLVersion = 0x02 // Vulnerable to "POODLE" attack.
	TLSv1   SSLVersion = 0x03
	TLSv1_1 SSLVersion = 0x04
	TLSv1_2 SSLVersion = 0x05

	// Make sure to disable SSLv2 and SSLv3 if you use this. SSLv3 is vulnerable
	// to the "POODLE" attack, and SSLv2 is what, just don't even.
	AnyVersion SSLVersion = 0x06
)

// NewCtxWithVersion creates an SSL context that is specific to the provided
// SSL version. See http://www.openssl.org/docs/ssl/SSL_CTX_new.html for more.
func NewCtxWithVersion(version SSLVersion) (*Ctx, error) {
	var method *C.SSL_METHOD
	switch version {
	case SSLv3:
		method = C.X_SSLv3_method()
	case TLSv1:
		method = C.X_TLSv1_method()
	case TLSv1_1:
		method = C.X_TLSv1_1_method()
	case TLSv1_2:
		method = C.X_TLSv1_2_method()
	case AnyVersion:
		method = C.X_SSLv23_method()
	}
	if method == nil {
		return nil, errors.New("unknown ssl/tls version")
	}
	return newCtx(method)
}

// NewCtx creates a context that supports any TLS version 1.0 and newer.
func NewCtx() (*Ctx, error) {
	c, err := NewCtxWithVersion(AnyVersion)
	if err == nil {
		c.SetOptions(NoSSLv2 | NoSSLv3)
	}
	return c, err
}

// NewCtxFromFiles calls NewCtx, loads the provided files, and configures the
// context to use them.
func NewCtxFromFiles(cert_file string, key_file string) (*Ctx, error) {
	ctx, err := NewCtx()
	if err != nil {
		return nil, err
	}

	cert_bytes, err := ioutil.ReadFile(cert_file)
	if err != nil {
		return nil, err
	}

	certs := SplitPEM(cert_bytes)
	if len(certs) == 0 {
		return nil, fmt.Errorf("No PEM certificate found in '%s'", cert_file)
	}
	first, certs := certs[0], certs[1:]
	cert, err := LoadCertificateFromPEM(first)
	if err != nil {
		return nil, err
	}

	err = ctx.UseCertificate(cert)
	if err != nil {
		return nil, err
	}

	for _, pem := range certs {
		cert, err := LoadCertificateFromPEM(pem)
		if err != nil {
			return nil, err
		}
		err = ctx.AddChainCertificate(cert)
		if err != nil {
			return nil, err
		}
	}

	key_bytes, err := ioutil.ReadFile(key_file)
	if err != nil {
		return nil, err
	}

	key, err := LoadPrivateKeyFromPEM(key_bytes)
	if err != nil {
		return nil, err
	}

	err = ctx.UsePrivateKey(key)
	if err != nil {
		return nil, err
	}

	return ctx, nil
}

// EllipticCurve repesents the ASN.1 OID of an elliptic curve.
// see https://www.openssl.org/docs/apps/ecparam.html for a list of implemented curves.
type EllipticCurve int

const (
	// P-256: X9.62/SECG curve over a 256 bit prime field
	Prime256v1 EllipticCurve = C.NID_X9_62_prime256v1
	// P-384: NIST/SECG curve over a 384 bit prime field
	Secp384r1 EllipticCurve = C.NID_secp384r1
	// P-521: NIST/SECG curve over a 521 bit prime field
	Secp521r1 EllipticCurve = C.NID_secp521r1
)

// SetEllipticCurve sets the elliptic curve used by the SSL context to
// enable an ECDH cipher suite to be selected during the handshake.
func (c *Ctx) SetEllipticCurve(curve EllipticCurve) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	k := C.EC_KEY_new_by_curve_name(C.int(curve))
	if k == nil {
		return errors.New("Unknown curve")
	}
	defer C.EC_KEY_free(k)

	if int(C.X_SSL_CTX_set_tmp_ecdh(c.ctx, k)) != 1 {
		return errorFromErrorQueue()
	}

	return nil
}

// UseCertificate configures the context to present the given certificate to
// peers.
func (c *Ctx) UseCertificate(cert *Certificate) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c.cert = cert
	if int(C.SSL_CTX_use_certificate(c.ctx, cert.x)) != 1 {
		return errorFromErrorQueue()
	}
	return nil
}

// AddChainCertificate adds a certificate to the chain presented in the
// handshake.
func (c *Ctx) AddChainCertificate(cert *Certificate) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c.chain = append(c.chain, cert)
	if int(C.X_SSL_CTX_add_extra_chain_cert(c.ctx, cert.x)) != 1 {
		return errorFromErrorQueue()
	}
	// OpenSSL takes ownership via SSL_CTX_add_extra_chain_cert
	runtime.SetFinalizer(cert, nil)
	return nil
}

// UsePrivateKey configures the context to use the given private key for SSL
// handshakes.
func (c *Ctx) UsePrivateKey(key PrivateKey) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	c.key = key
	if int(C.SSL_CTX_use_PrivateKey(c.ctx, key.evpPKey())) != 1 {
		return errorFromErrorQueue()
	}
	return nil
}

type CertificateStore struct {
	store *C.X509_STORE
	// for GC
	ctx   *Ctx
	certs []*Certificate
}

// Allocate a new, empty CertificateStore
func NewCertificateStore() (*CertificateStore, error) {
	s := C.X509_STORE_new()
	if s == nil {
		return nil, errors.New("failed to allocate X509_STORE")
	}
	store := &CertificateStore{store: s}
	runtime.SetFinalizer(store, func(s *CertificateStore) {
		C.X509_STORE_free(s.store)
	})
	return store, nil
}

// Parse a chained PEM file, loading all certificates into the Store.
func (s *CertificateStore) LoadCertificatesFromPEM(data []byte) error {
	pems := SplitPEM(data)
	for _, pem := range pems {
		cert, err := LoadCertificateFromPEM(pem)
		if err != nil {
			return err
		}
		err = s.AddCertificate(cert)
		if err != nil {
			return err
		}
	}
	return nil
}

// GetCertificateStore returns the context's certificate store that will be
// used for peer validation.
func (c *Ctx) GetCertificateStore() *CertificateStore {
	// we don't need to dealloc the cert store pointer here, because it points
	// to a ctx internal. so we do need to keep the ctx around
	return &CertificateStore{
		store: C.SSL_CTX_get_cert_store(c.ctx),
		ctx:   c}
}

// AddCertificate marks the provided Certificate as a trusted certificate in
// the given CertificateStore.
func (s *CertificateStore) AddCertificate(cert *Certificate) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s.certs = append(s.certs, cert)
	if int(C.X509_STORE_add_cert(s.store, cert.x)) != 1 {
		return errorFromErrorQueue()
	}
	return nil
}

type CertificateStoreCtx struct {
	ctx     *C.X509_STORE_CTX
	ssl_ctx *Ctx
}

func (self *CertificateStoreCtx) VerifyResult() VerifyResult {
	return VerifyResult(C.X509_STORE_CTX_get_error(self.ctx))
}

func (self *CertificateStoreCtx) Err() error {
	code := C.X509_STORE_CTX_get_error(self.ctx)
	if code == C.X509_V_OK {
		return nil
	}
	return fmt.Errorf("openssl: %s",
		C.GoString(C.X509_verify_cert_error_string(C.long(code))))
}

func (self *CertificateStoreCtx) Depth() int {
	return int(C.X509_STORE_CTX_get_error_depth(self.ctx))
}

// the certicate returned is only valid for the lifetime of the underlying
// X509_STORE_CTX
func (self *CertificateStoreCtx) GetCurrentCert() *Certificate {
	x509 := C.X509_STORE_CTX_get_current_cert(self.ctx)
	if x509 == nil {
		return nil
	}
	// add a ref
	if 1 != C.X_X509_add_ref(x509) {
		return nil
	}
	cert := &Certificate{
		x: x509,
	}
	runtime.SetFinalizer(cert, func(cert *Certificate) {
		C.X509_free(cert.x)
	})
	return cert
}

// LoadVerifyLocations tells the context to trust all certificate authorities
// provided in either the ca_file or the ca_path.
// See http://www.openssl.org/docs/ssl/SSL_CTX_load_verify_locations.html for
// more.
func (c *Ctx) LoadVerifyLocations(ca_file string, ca_path string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var c_ca_file, c_ca_path *C.char
	if ca_file != "" {
		c_ca_file = C.CString(ca_file)
		defer C.free(unsafe.Pointer(c_ca_file))
	}
	if ca_path != "" {
		c_ca_path = C.CString(ca_path)
		defer C.free(unsafe.Pointer(c_ca_path))
	}
	if C.SSL_CTX_load_verify_locations(c.ctx, c_ca_file, c_ca_path) != 1 {
		return errorFromErrorQueue()
	}
	return nil
}

type Options int

const (
	// NoCompression is only valid if you are using OpenSSL 1.0.1 or newer
	NoCompression                      Options = C.SSL_OP_NO_COMPRESSION
	NoSSLv2                            Options = C.SSL_OP_NO_SSLv2
	NoSSLv3                            Options = C.SSL_OP_NO_SSLv3
	NoTLSv1                            Options = C.SSL_OP_NO_TLSv1
	CipherServerPreference             Options = C.SSL_OP_CIPHER_SERVER_PREFERENCE
	NoSessionResumptionOrRenegotiation Options = C.SSL_OP_NO_SESSION_RESUMPTION_ON_RENEGOTIATION
	NoTicket                           Options = C.SSL_OP_NO_TICKET
)

// SetOptions sets context options. See
// http://www.openssl.org/docs/ssl/SSL_CTX_set_options.html
func (c *Ctx) SetOptions(options Options) Options {
	return Options(C.X_SSL_CTX_set_options(
		c.ctx, C.long(options)))
}

func (c *Ctx) ClearOptions(options Options) Options {
	return Options(C.X_SSL_CTX_clear_options(
		c.ctx, C.long(options)))
}

// GetOptions returns context options. See
// https://www.openssl.org/docs/ssl/SSL_CTX_set_options.html
func (c *Ctx) GetOptions() Options {
	return Options(C.X_SSL_CTX_get_options(c.ctx))
}

type Modes int

const (
	// ReleaseBuffers is only valid if you are using OpenSSL 1.0.1 or newer
	ReleaseBuffers Modes = C.SSL_MODE_RELEASE_BUFFERS
)

// SetMode sets context modes. See
// http://www.openssl.org/docs/ssl/SSL_CTX_set_mode.html
func (c *Ctx) SetMode(modes Modes) Modes {
	return Modes(C.X_SSL_CTX_set_mode(c.ctx, C.long(modes)))
}

// GetMode returns context modes. See
// http://www.openssl.org/docs/ssl/SSL_CTX_set_mode.html
func (c *Ctx) GetMode() Modes {
	return Modes(C.X_SSL_CTX_get_mode(c.ctx))
}

type VerifyOptions int

const (
	VerifyNone             VerifyOptions = C.SSL_VERIFY_NONE
	VerifyPeer             VerifyOptions = C.SSL_VERIFY_PEER
	VerifyFailIfNoPeerCert VerifyOptions = C.SSL_VERIFY_FAIL_IF_NO_PEER_CERT
	VerifyClientOnce       VerifyOptions = C.SSL_VERIFY_CLIENT_ONCE
)

type VerifyCallback func(ok bool, store *CertificateStoreCtx) bool

//export go_ssl_ctx_verify_cb_thunk
func go_ssl_ctx_verify_cb_thunk(p unsafe.Pointer, ok C.int, ctx *C.X509_STORE_CTX) C.int {
	defer func() {
		if err := recover(); err != nil {
			logger.Critf("openssl: verify callback panic'd: %v", err)
			os.Exit(1)
		}
	}()
	verify_cb := (*Ctx)(p).verify_cb
	// set up defaults just in case verify_cb is nil
	if verify_cb != nil {
		store := &CertificateStoreCtx{ctx: ctx}
		if verify_cb(ok == 1, store) {
			ok = 1
		} else {
			ok = 0
		}
	}
	return ok
}

// SetVerify controls peer verification settings. See
// http://www.openssl.org/docs/ssl/SSL_CTX_set_verify.html
func (c *Ctx) SetVerify(options VerifyOptions, verify_cb VerifyCallback) {
	c.verify_cb = verify_cb
	if verify_cb != nil {
		C.SSL_CTX_set_verify(c.ctx, C.int(options), (*[0]byte)(C.X_SSL_CTX_verify_cb))
	} else {
		C.SSL_CTX_set_verify(c.ctx, C.int(options), nil)
	}
}

func (c *Ctx) SetVerifyMode(options VerifyOptions) {
	c.SetVerify(options, c.verify_cb)
}

func (c *Ctx) SetVerifyCallback(verify_cb VerifyCallback) {
	c.SetVerify(c.VerifyMode(), verify_cb)
}

func (c *Ctx) GetVerifyCallback() VerifyCallback {
	return c.verify_cb
}

func (c *Ctx) VerifyMode() VerifyOptions {
	return VerifyOptions(C.SSL_CTX_get_verify_mode(c.ctx))
}

// SetVerifyDepth controls how many certificates deep the certificate
// verification logic is willing to follow a certificate chain. See
// https://www.openssl.org/docs/ssl/SSL_CTX_set_verify.html
func (c *Ctx) SetVerifyDepth(depth int) {
	C.SSL_CTX_set_verify_depth(c.ctx, C.int(depth))
}

// GetVerifyDepth controls how many certificates deep the certificate
// verification logic is willing to follow a certificate chain. See
// https://www.openssl.org/docs/ssl/SSL_CTX_set_verify.html
func (c *Ctx) GetVerifyDepth() int {
	return int(C.SSL_CTX_get_verify_depth(c.ctx))
}

type TLSExtServernameCallback func(ssl *SSL) SSLTLSExtErr

// SetTLSExtServernameCallback sets callback function for Server Name Indication
// (SNI) rfc6066 (http://tools.ietf.org/html/rfc6066). See
// http://stackoverflow.com/questions/22373332/serving-multiple-domains-in-one-box-with-sni
func (c *Ctx) SetTLSExtServernameCallback(sni_cb TLSExtServernameCallback) {
	c.sni_cb = sni_cb
	C.X_SSL_CTX_set_tlsext_servername_callback(c.ctx, (*[0]byte)(C.sni_cb))
}

func (c *Ctx) SetSessionId(session_id []byte) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var ptr *C.uchar
	if len(session_id) > 0 {
		ptr = (*C.uchar)(unsafe.Pointer(&session_id[0]))
	}
	if int(C.SSL_CTX_set_session_id_context(c.ctx, ptr,
		C.uint(len(session_id)))) == 0 {
		return errorFromErrorQueue()
	}
	return nil
}

// SetCipherList sets the list of available ciphers. The format of the list is
// described at http://www.openssl.org/docs/apps/ciphers.html, but see
// http://www.openssl.org/docs/ssl/SSL_CTX_set_cipher_list.html for more.
func (c *Ctx) SetCipherList(list string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	clist := C.CString(list)
	defer C.free(unsafe.Pointer(clist))
	if int(C.SSL_CTX_set_cipher_list(c.ctx, clist)) == 0 {
		return errorFromErrorQueue()
	}
	return nil
}

// SetNextProtos sets Negotiation protocol to the ctx.
func (c *Ctx) SetNextProtos(protos []string) error {
	if len(protos) == 0 {
		return nil
	}
	vector := make([]byte, 0)
	for _, proto := range protos {
		if len(proto) > 255 {
			return fmt.Errorf(
				"Proto length can't be more than 255. But got a proto %s with length %d",
				proto, len(proto))
		}
		vector = append(vector, byte(uint8(len(proto))))
		vector = append(vector, []byte(proto)...)
	}
	ret := int(C.SSL_CTX_set_alpn_protos(c.ctx, (*C.uchar)(unsafe.Pointer(&vector[0])),
		C.uint(len(vector))))
	if ret != 0 {
		return errors.New("Error while setting protos to ctx")
	}
	return nil
}

type SessionCacheModes int

const (
	SessionCacheOff    SessionCacheModes = C.SSL_SESS_CACHE_OFF
	SessionCacheClient SessionCacheModes = C.SSL_SESS_CACHE_CLIENT
	SessionCacheServer SessionCacheModes = C.SSL_SESS_CACHE_SERVER
	SessionCacheBoth   SessionCacheModes = C.SSL_SESS_CACHE_BOTH
	NoAutoClear        SessionCacheModes = C.SSL_SESS_CACHE_NO_AUTO_CLEAR
	NoInternalLookup   SessionCacheModes = C.SSL_SESS_CACHE_NO_INTERNAL_LOOKUP
	NoInternalStore    SessionCacheModes = C.SSL_SESS_CACHE_NO_INTERNAL_STORE
	NoInternal         SessionCacheModes = C.SSL_SESS_CACHE_NO_INTERNAL
)

// SetSessionCacheMode enables or disables session caching. See
// http://www.openssl.org/docs/ssl/SSL_CTX_set_session_cache_mode.html
func (c *Ctx) SetSessionCacheMode(modes SessionCacheModes) SessionCacheModes {
	return SessionCacheModes(
		C.X_SSL_CTX_set_session_cache_mode(c.ctx, C.long(modes)))
}

// Set session cache timeout. Returns previously set value.
// See https://www.openssl.org/docs/ssl/SSL_CTX_set_timeout.html
func (c *Ctx) SetTimeout(t time.Duration) time.Duration {
	prev := C.X_SSL_CTX_set_timeout(c.ctx, C.long(t/time.Second))
	return time.Duration(prev) * time.Second
}

// Get session cache timeout.
// See https://www.openssl.org/docs/ssl/SSL_CTX_set_timeout.html
func (c *Ctx) GetTimeout() time.Duration {
	return time.Duration(C.X_SSL_CTX_get_timeout(c.ctx)) * time.Second
}

// Set session cache size. Returns previously set value.
// https://www.openssl.org/docs/ssl/SSL_CTX_sess_set_cache_size.html
func (c *Ctx) SessSetCacheSize(t int) int {
	return int(C.X_SSL_CTX_sess_set_cache_size(c.ctx, C.long(t)))
}

// Get session cache size.
// https://www.openssl.org/docs/ssl/SSL_CTX_sess_set_cache_size.html
func (c *Ctx) SessGetCacheSize() int {
	return int(C.X_SSL_CTX_sess_get_cache_size(c.ctx))
}
