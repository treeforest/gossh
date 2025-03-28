package gossh

import (
	"github.com/stretchr/testify/require"
	"github.com/treeforest/golog"
	"testing"
)

var conn *SSH

func TestMain(m *testing.M) {
	var err error
	conn, err = Connect("192.168.1.20", 22, "tony", "1234qwer.")
	if err != nil {
		panic(err)
	}

	defer conn.Close()

	l := golog.NewLogger(nil)
	conn.Log = l.Debugf

	m.Run()
}

func TestSSH_UserHomeDir(t *testing.T) {
	home, err := conn.UserHomeDir()
	require.NoError(t, err)
	t.Logf("home dir: %s", home)
}

func TestSSH_Upload(t *testing.T) {
	err := conn.Upload("./ssh.go", "~/")
	require.NoError(t, err)
	err = conn.Upload("./tmp", "/home/tony/tmp")
	require.NoError(t, err)
}

func TestSSH_Download(t *testing.T) {
	err := conn.Download("~/tmp", "./tmp")
	require.NoError(t, err)
}

func Test_deleteFileLines(t *testing.T) {
	err := deleteFileLines("test.txt", []int{2})
	require.NoError(t, err)
}
