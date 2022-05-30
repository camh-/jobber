package cli

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"

	"google.golang.org/grpc/credentials"
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
