package remote

import (
	transportssh "github.com/weill-labs/amux/internal/transport/ssh"
	"golang.org/x/crypto/ssh"
)

func DeployBinary(client *ssh.Client, buildHash string) error {
	return transportssh.DeployBinary(client, buildHash)
}
