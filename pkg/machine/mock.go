package machine

//go:generate go run go.uber.org/mock/mockgen -destination=./csmock/server_service.go -package csmock github.com/cloudscale-ch/cloudscale-go-sdk/v6 ServerService
//go:generate go run go.uber.org/mock/mockgen -destination=./csmock/server_group_service.go -package csmock github.com/cloudscale-ch/cloudscale-go-sdk/v6 ServerGroupService
