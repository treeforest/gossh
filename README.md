# GoSSH

GoSSH is a Go package that provides a simple and convenient way to establish SSH connections and perform various operations on remote servers.

## Installation

To install GoSSH, use the following command:

```bash
go get -u github.com/treeforest/gossh
```

## Usage

Here is an example of how to use GoSSH to connect to a remote server and perform SSH operations:

```go
package main

import (
	"fmt"
	"github.com/treeforest/gossh"
)

func main() {
	// Connect to the remote server
	ssh, err := gossh.Connect("example.com", 22, "username", "password")
	if err != nil {
		fmt.Println("Failed to connect:", err)
		return
	}
	defer ssh.Close()

	// Run a command on the remote server
	output, err := ssh.Run("ls -l")
	if err != nil {
		fmt.Println("Failed to run command:", err)
		return
	}

	fmt.Println("Command output:", string(output))
}
```

## Features

- Connect to a remote server using SSH
- Run commands on the remote server
- Upload and download files using SFTP
- Execute commands with sudo privileges

## License

GoSSH is released under the MIT License. See the [LICENSE](https://github.com/treeforest/gossh/blob/main/LICENSE) file for more information.

---

This README document provides a brief overview of the GoSSH package and its usage. Feel free to update and customize it according to your specific needs.