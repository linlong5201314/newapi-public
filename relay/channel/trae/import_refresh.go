package trae

import "context"

// RefreshImportedCredential performs the provider's device-bound exchange for
// an imported Magpie session and returns the complete persistable credential.
// The channel's normal refresh path then reuses the same keypair and protocol.
func RefreshImportedCredential(ctx context.Context, proxy string, prior *Cred, publicKey, privateKey string) (*Cred, error) {
	credential := *prior
	credential.DevicePublicKey = publicKey
	credential.DevicePrivateKey = privateKey
	return NewClient(&credential).refreshTokenOnce()
}
