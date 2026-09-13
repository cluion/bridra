module github.com/cluion/bridra/tool/gomobile

go 1.25.0

require github.com/cluion/bridra/backend v0.0.0-00010101000000-000000000000

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/redis/go-redis/v9 v9.21.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/mobile v0.0.0-20260709172247-6129f5bee9d5 // indirect
	golang.org/x/mod v0.38.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/tools v0.48.0 // indirect
)

replace github.com/cluion/bridra/backend => ../../backend

tool (
	golang.org/x/mobile/cmd/gobind
	golang.org/x/mobile/cmd/gomobile
)
