// CI-ONLY module. Never built into any container image.
//
// Module 3 LB-1. It exists so the mint↔verify contract can be proven against
// BOTH real implementations without either deployable service depending on the
// other.
//
// The previous arrangement put this dependency in identity-auth-service's
// production go.mod with a source-local replace pointing at
// ../../../Architecture/services/api-gateway. The auth Dockerfile copies only
// `shared/` and `services/auth-service/` into its build context, so that path
// does not exist inside the image and `go mod download` fails. The contract was
// real but the coupling was in the wrong place.
//
// Nothing here is imported by production code. Both replaces point at
// repository paths that exist only in a full checkout, which is exactly the
// environment CI runs in and exactly the environment an image build is not.
module github.com/atpost/contracts/edge-auth

go 1.25.0

require (
	github.com/atpost/api-gateway v0.0.0
	github.com/atpost/chat-shared v0.0.0
	github.com/atpost/identity-auth-service v0.0.0
	github.com/google/uuid v1.6.0
)

require github.com/golang-jwt/jwt/v5 v5.3.1 // indirect

replace github.com/atpost/api-gateway => ../../../Architecture/services/api-gateway

replace github.com/atpost/chat-shared => ../../../chat-service/shared

replace github.com/atpost/identity-auth-service => ../../../identity-platform/services/auth-service

replace github.com/atpost/identity-shared => ../../../identity-platform/shared

require github.com/atpost/graph-service v0.0.0

replace github.com/atpost/graph-service => ../../../Architecture/services/graph-service

replace github.com/atpost/shared => ../../../Architecture/shared
