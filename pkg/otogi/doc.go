// Package otogi defines the protocol-neutral event model and the module SDK
// shared by the kernel, modules, and platform drivers.
//
// Values are plain structs with JSON tags; encoding/json is their canonical
// serialization. After handing a value to another component, do not mutate it.
package otogi
