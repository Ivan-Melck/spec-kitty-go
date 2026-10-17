//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

// Fixed normal map binding. No environment, working-directory, profile
// selector, optional hash, or fallback source is accepted. Not a production API.
const (
 h06LateSelectedProfile = "normal"
 h06LateSelectedSourcePath = "/home/user/Documents/code/spec-kitty-redesign/internal/productionruntime/expiry_qualification_rundown.go"
 h06LateSelectedSourceSHA256 = "ae2f036949803725a4c5c3b21c4e20746a9d06765f71930cf56aa7ac421101c2"
)
