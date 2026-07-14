package update

// Blank import pins minio/selfupdate in go.mod until apply.go (lane L5)
// imports it for real; L5 deletes this file.
import _ "github.com/minio/selfupdate"
