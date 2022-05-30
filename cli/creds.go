package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"

	"github.com/camh-/jobber/job"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

var (
	ErrAuthFailed   = errors.New("authentication failed")
	ErrNoPeer       = fmt.Errorf("%w: no peer in context", ErrAuthFailed)
	ErrNoTLSInfo    = fmt.Errorf("%w: no TLSInfo auth info", ErrAuthFailed)
	ErrNoClientCert = fmt.Errorf("%w: no client certificate in auth info", ErrAuthFailed)
	ErrNoCNInCert   = fmt.Errorf("%w: no CN in client certificate", ErrAuthFailed)
)

func mTLSCreds(certFile, keyFile, caFile string) (credentials.TransportCredentials, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	caCert, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("could not load ca certs from %s", caFile)
	}

	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool, // make it work on both client and server
		ClientCAs:    caCertPool, // make it work on both client and server
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
		// cipher suites are not configurable with TLS13
	}
	return credentials.NewTLS(cfg), nil
}

func CNToUser(ctx context.Context) (context.Context, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, ErrNoPeer
	}

	authinfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil, ErrNoTLSInfo
	}

	if len(authinfo.State.PeerCertificates) == 0 {
		return nil, ErrNoClientCert
	}

	cert := authinfo.State.PeerCertificates[0]
	cn := cert.Subject.CommonName
	if cn == "" {
		return nil, ErrNoCNInCert
	}

	return job.AddUserToContext(ctx, cn), nil
}
