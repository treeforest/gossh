package gossh

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gookit/goutil/fsutil"
	"github.com/melbahja/goph"
	"github.com/pkg/errors"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Logger func(format string, v ...interface{})

func Connect(host string, port uint, username, password string) (*SSH, error) {
	if port == 0 {
		port = 22
	}

	s := &SSH{
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
	}

	err := s.connect()
	if err != nil {
		return nil, errors.WithStack(err)
	}

	return s, nil
}

type SSH struct {
	Client   *goph.Client
	Host     string
	Port     uint
	Username string
	Password string
	Log      Logger
}

func (s *SSH) Close() error {
	return s.Client.Close()
}

func (s *SSH) Ping() error {
	_, err := s.Client.Run("ls > /dev/null")
	return err
}

func (s *SSH) Run(cmd string) ([]byte, error) {
	s.Log("$ %s", cmd)
	return s.Client.Run(cmd)
}

func (s *SSH) Runf(format string, a ...interface{}) ([]byte, error) {
	return s.Run(fmt.Sprintf(format, a...))
}

func (s *SSH) Sudo(cmd string) ([]byte, error) {
	if s.Username != "root" {
		cmd = fmt.Sprintf(`echo '%s' | sudo -S sh -c "%s"`, s.Password, cmd)
	}
	return s.Run(cmd)
}

func (s *SSH) Sudof(format string, a ...interface{}) ([]byte, error) {
	return s.Sudo(fmt.Sprintf(format, a...))
}

func (s *SSH) Sftp() (*sftp.Client, error) {
	return s.Client.NewSftp()
}

func (s *SSH) UserHomeDir() (string, error) {
	out, err := s.Run("echo $HOME")
	if err != nil {
		return "", err
	}
	return string(bytes.TrimRight(out, "\n")), nil
}

func (s *SSH) Chown(user string, path string) error {
	_, err := s.Runf("chown %s %s", user, path)
	return err
}

func (s *SSH) PathExists(path string) (bool, error) {
	sftpClient, err := s.Client.NewSftp()
	if err != nil {
		return false, errors.WithMessagef(err, "failed to create SFTP client: %v", err)
	}
	defer sftpClient.Close()
	return s.pathExists(sftpClient, path), nil
}

func (s *SSH) Download(remotePath, localDir string) (err error) {
	if fsutil.IsFile(localDir) {
		return errors.Errorf("local directory '%s' cannot be a file", localDir)
	}

	remotePath, err = s.expandTilde(strings.TrimSuffix(remotePath, "/"))
	if err != nil {
		return errors.WithMessagef(err, "failed to expand '~' in remote directory: %v", err)
	}

	sftpClient, err := s.Client.NewSftp()
	if err != nil {
		return errors.WithMessagef(err, "failed to create SFTP client: %v", err)
	}
	defer sftpClient.Close()

	if !s.pathExists(sftpClient, remotePath) {
		return errors.Errorf("remote path '%s' not found", remotePath)
	}

	if s.isFile(sftpClient, remotePath) {
		err = os.MkdirAll(localDir, 0755)
		if err != nil {
			return errors.WithMessagef(err, "failed to create local directory '%s': %v", localDir, err)
		}
		return s.downloadFile(sftpClient, remotePath, filepath.Join(localDir, filepath.Base(remotePath)))
	}

	return s.downloadDirectory(sftpClient, remotePath, localDir)
}

func (s *SSH) downloadDirectory(sftpClient *sftp.Client, remoteDir, localDir string) error {
	s.Log("download directory %s -> %s", remoteDir, localDir)

	entries, err := sftpClient.ReadDir(remoteDir)
	if err != nil {
		return errors.WithMessagef(err, "failed to read remote directory '%s': %v", remoteDir, err)
	}

	for _, entry := range entries {
		local := filepath.Join(localDir, entry.Name())
		remote := filepath.Join(remoteDir, entry.Name())
		if entry.IsDir() {
			err = os.MkdirAll(local, entry.Mode())
			if err != nil {
				return errors.WithMessagef(err, "failed to create local directory '%s': %v", local, err)
			}
			err = s.downloadDirectory(sftpClient, remote, local)
			if err != nil {
				return errors.WithMessagef(err, "failed to download directory '%s': %v", remote, err)
			}
		} else {
			err = s.downloadFile(sftpClient, remote, local)
			if err != nil {
				return errors.WithMessagef(err, "failed to download file '%s': %v", remote, err)
			}
		}
	}
	return nil
}

func (s *SSH) downloadFile(sftpClient *sftp.Client, remotePath, localPath string) error {
	s.Log("download file %s -> %s", remotePath, localPath)

	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return errors.WithMessagef(err, "failed to open remote file '%s': %v", remotePath, err)
	}
	defer remoteFile.Close()

	remoteFileInfo, err := remoteFile.Stat()
	if err != nil {
		return errors.WithMessagef(err, "failed to get remote file info: %v", err)
	}

	localFile, err := os.OpenFile(localPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, remoteFileInfo.Mode())
	if err != nil {
		return errors.WithMessagef(err, "failed to create local file '%s': %v", localPath, err)
	}
	defer localFile.Close()

	_, err = io.Copy(localFile, remoteFile)
	if err != nil {
		return errors.WithMessagef(err, "failed to copy remote file to local: %v", err)
	}

	return nil
}

func (s *SSH) Upload(localPath, remoteDir string) (err error) {
	if !fsutil.PathExists(localPath) {
		return errors.Errorf("local path '%s' not found", localPath)
	}

	remoteDir, err = s.expandTilde(strings.TrimSuffix(remoteDir, "/"))
	if err != nil {
		return errors.WithMessagef(err, "failed to expand '~' in remote directory: %v", err)
	}

	sftpClient, err := s.Client.NewSftp()
	if err != nil {
		return errors.WithMessagef(err, "failed to create SFTP client: %v", err)
	}
	defer sftpClient.Close()

	if s.isFile(sftpClient, remoteDir) {
		return errors.Errorf("remote directory '%s' cannot be a file", remoteDir)
	}

	if fsutil.IsFile(localPath) {
		err = sftpClient.MkdirAll(remoteDir)
		if err != nil {
			return errors.WithMessagef(err, "failed to create remote directory '%s': %v", remoteDir, err)
		}
		return s.uploadFile(sftpClient, localPath, filepath.Join(remoteDir, filepath.Base(localPath)))
	}

	return s.uploadDirectory(sftpClient, localPath, remoteDir)
}

func (s *SSH) uploadDirectory(sftpClient *sftp.Client, localDir, remoteDir string) error {
	s.Log("upload directory %s -> %s", localDir, remoteDir)

	entries, err := os.ReadDir(localDir)
	if err != nil {
		return errors.WithMessagef(err, "failed to read local directory '%s': %v", localDir, err)
	}
	for _, entry := range entries {
		local := filepath.Join(localDir, entry.Name())
		remote := filepath.Join(remoteDir, entry.Name())
		if entry.IsDir() {
			err = sftpClient.MkdirAll(remote)
			if err != nil {
				return errors.WithMessagef(err, "failed to create remote directory '%s': %v", remote, err)
			}
			err = s.uploadDirectory(sftpClient, local, remote)
			if err != nil {
				return errors.WithMessagef(err, "failed to upload directory '%s': %v", local, err)
			}
		} else {
			err = s.uploadFile(sftpClient, local, remote)
			if err != nil {
				return errors.WithMessagef(err, "failed to upload file '%s': %v", local, err)
			}
		}
	}
	return nil
}

func (s *SSH) uploadFile(sftpClient *sftp.Client, localPath, remotePath string) error {
	s.Log("upload file %s -> %s", localPath, remotePath)

	localFile, err := os.Open(localPath)
	if err != nil {
		return errors.WithMessagef(err, "failed to open local file '%s': %v", localPath, err)
	}
	defer localFile.Close()

	localFileInfo, err := localFile.Stat()
	if err != nil {
		return errors.WithMessagef(err, "failed to get local file info: %v", err)
	}

	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return errors.WithMessagef(err, "failed to create remote file '%s': %v", remotePath, err)
	}
	defer remoteFile.Close()

	err = remoteFile.Chmod(localFileInfo.Mode())
	if err != nil {
		return errors.WithMessagef(err, "failed to set remote file permissions: %v", err)
	}

	_, err = io.Copy(remoteFile, localFile)
	if err != nil {
		return errors.WithMessagef(err, "failed to copy local file to remote: %v", err)
	}

	return nil
}

// 转义 ~
func (s *SSH) expandTilde(path string) (string, error) {
	if !strings.HasPrefix(path, "~") {
		return path, nil
	}

	homeDir, err := s.UserHomeDir()
	if err != nil {
		return "", errors.WithStack(err)
	}

	expandedPath := strings.Replace(path, "~", homeDir, 1)
	return expandedPath, nil
}

func (s *SSH) isFile(sftpClient *sftp.Client, path string) bool {
	if fi, err := sftpClient.Stat(path); err == nil {
		return !fi.IsDir()
	}
	return false
}

func (s *SSH) isDir(sftpClient *sftp.Client, path string) bool {
	if fi, err := os.Stat(path); err == nil {
		return fi.IsDir()
	}
	return false
}

func (s *SSH) pathExists(sftpClient *sftp.Client, path string) bool {
	if _, err := sftpClient.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func (s *SSH) connect() error {
	// 测试远程连接是否可通
	address := fmt.Sprintf("%s:%d", s.Host, s.Port)
	if _, err := isReachableAddr(address); err != nil {
		return errors.WithStack(err)
	}

	// knownFile
	knownFile, err := getKnownFile()
	if err != nil {
		return errors.WithStack(err)
	}

	// 发起 ssh 连接
	client, err := goph.NewConn(&goph.Config{
		Addr: s.Host,
		Port: s.Port,
		User: s.Username,
		Auth: goph.Password(s.Password),
		Callback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			hostFound, err := goph.CheckKnownHost(hostname, remote, key, knownFile)
			if hostFound && err != nil {
				var keyErr *knownhosts.KeyError
				var revokedErr *knownhosts.RevokedError
				if errors.As(err, &keyErr) {
					if len(keyErr.Want) == 0 {
						// 正常不会进入该语句。若 Want 为空，代表没有找到key，则 hostFound == false
						return errors.WithStack(err)
					}
					// 删除错误的 key
					lines := make([]int, 0)
					for _, want := range keyErr.Want {
						lines = append(lines, want.Line)
					}
					if err = deleteFileLines(knownFile, lines); err != nil {
						return errors.WithStack(err)
					}
				} else if errors.As(err, revokedErr) {
					// 删除被撤销的 key
					if err = deleteFileLines(knownFile, []int{revokedErr.Revoked.Line}); err != nil {
						return errors.WithStack(err)
					}
				} else {
					return errors.WithMessage(err, "check knownhost error")
				}
			}

			if hostFound && err == nil {
				// 已存在
				return nil
			}

			if err = goph.AddKnownHost(hostname, remote, key, knownFile); err != nil {
				return errors.WithStack(err)
			}
			return nil
		},
	})
	if err != nil {
		return errors.WithStack(err)
	}

	s.Client = client
	return nil
}

func getKnownFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errors.WithMessage(err, "failed to get user home directory")
	}

	sshDir := filepath.Join(home, ".ssh")
	err = os.MkdirAll(sshDir, 0755)
	if err != nil {
		return "", errors.WithMessage(err, "failed to create .ssh directory")
	}

	knownFile := filepath.Join(sshDir, "known_hosts")
	return knownFile, nil
}

func isReachableAddr(address string) (bool, error) {
	conn, err := net.DialTimeout("tcp", address, time.Second*2)
	if err != nil {
		return false, err
	}
	_ = conn.Close()
	return true, nil
}

func deleteFileLines(filename string, linesToDelete []int) error {
	// Open the original file for reading
	file, err := os.OpenFile(filename, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file: %v", err)
	}
	defer file.Close()

	// Read the content of the original file and save the lines to keep in a new string slice
	scanner := bufio.NewScanner(file)
	var linesToKeep []string
	lineNumber := 1
	for scanner.Scan() {
		if !contains(linesToDelete, lineNumber) {
			linesToKeep = append(linesToKeep, scanner.Text())
		}
		lineNumber++
	}

	if err = scanner.Err(); err != nil {
		return fmt.Errorf("failed to read file: %v", err)
	}

	// Join the lines from the new string slice into a single string
	newContent := strings.Join(linesToKeep, "\n")

	// Write the reassembled string back to the original file, overwriting the original content
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate file: %v", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek file: %v", err)
	}
	if _, err := file.WriteString(newContent); err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	return nil
}

func contains(slice []int, value int) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}
