package remote

import "golang.org/x/crypto/ssh"

func sshRun(client *ssh.Client, cmd string) {
	sess, err := client.NewSession()
	if err != nil {
		return
	}
	defer sess.Close()
	_ = sess.Run(cmd)
}
