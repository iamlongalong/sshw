#!/bin/bash
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o build/sshw-darwin-amd64 cmd/sshw/main.go
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o build/sshw-darwin-arm64 cmd/sshw/main.go

CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -o build/sshw-windows-386 cmd/sshw/main.go
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o build/sshw-windows-amd64 cmd/sshw/main.go

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o build/sshw-linux-amd64 cmd/sshw/main.go
CGO_ENABLED=0 GOOS=linux GOARCH=386 go build -o build/sshw-linux-386 cmd/sshw/main.go
CGO_ENABLED=0 GOOS=linux GOARCH=arm go build -o build/sshw-linux-arm cmd/sshw/main.go
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o build/sshw-linux-arm64 cmd/sshw/main.go
CGO_ENABLED=0 GOOS=linux GOARCH=mips go build -o build/sshw-linux-mips cmd/sshw/main.go
CGO_ENABLED=0 GOOS=linux GOARCH=mips64 go build -o build/sshw-linux-mips64 cmd/sshw/main.go
