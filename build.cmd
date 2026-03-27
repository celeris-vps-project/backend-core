set GOOS=linux
set GOARCH=amd64
go build -o celeris-linux-amd64 cmd/api/main.go
go build -o celeris-linux-amd64-agent cmd/agent/main.go

set GOOS=windows
go build -o celeris-windows-amd64.exe cmd/api/main.go
go build -o celeris-linux-amd64-agent cmd/agent/main.go