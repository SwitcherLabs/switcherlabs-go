# SwitcherLabs Go SDK

SwitcherLabs is a feature flagging management platform that allows you to get 
started using feature flags in no time. The SwitcherLabs Go SDK allows you to
easily integrate feature flags in your Go projects.


## Installation

Make sure your project is using Go Modules (it will have a `go.mod` file in its
root if it already is):

``` sh
go mod init
```

Then, reference switcherlabs-go in a Go program with `import`:

``` go
import (
    "github.com/switcherlabs/switcherlabs-go/v1"
)
```

Run any of the normal `go` commands (`build`/`install`/`test`). The Go
toolchain will resolve and fetch the switcherlabs-go module automatically.

Alternatively, you can also explicitly `go get` the package into a project:

```
go get -u github.com/switcherlabs/switcherlabs-go/v1
```

## Usage


```go
import "github.com/switcherlabs/switcherlabs-go/v1"

client := switcherlabs.NewClient(&switcherlabs.Options{
    APIKey: "<YOUR_API_KEY HERE>",
})

flagEnabled, _ := client.BoolFlag(switcherlabs.FlagOptions{
    Key:        "user_123",
    Identifier: "new_feature_flag",
})
if err != nil {
    // handle err
}

if (flagEnabled) {
    // Do something if flag is enabled
} else {
    // Else do something else.
}
```
