package workbench

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Respond only with the permissions that Codex originally requested. Decode
// the supported wire shapes strictly so a newer permission kind requires an
// explicit UI/backend update before it can be granted.
func permissionDecision(raw json.RawMessage, decision string) (any, error) {
	grants := map[string]json.RawMessage{}
	response := map[string]any{"permissions": grants, "scope": "turn"}
	if decision == "decline" {
		return response, nil
	}
	if decision != "accept" {
		return nil, errors.New("invalid permission decision")
	}
	var request struct {
		Permissions map[string]json.RawMessage `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	unsupported := errors.New("unsupported requested permissions")
	for name, value := range request.Permissions {
		if name != "network" && name != "fileSystem" {
			return nil, unsupported
		}
		if string(value) == "null" {
			continue
		}
		d := json.NewDecoder(bytes.NewReader(value))
		d.DisallowUnknownFields()
		switch name {
		case "network":
			var network struct {
				Enabled *bool `json:"enabled"`
			}
			if d.Decode(&network) != nil || network.Enabled == nil {
				return nil, unsupported
			}
		case "fileSystem":
			var fs struct {
				Read             []string `json:"read"`
				Write            []string `json:"write"`
				GlobScanMaxDepth *uint    `json:"globScanMaxDepth"`
				Entries          []struct {
					Access string                               `json:"access"`
					Path   struct{ Type, Path, Pattern string } `json:"path"`
				} `json:"entries"`
			}
			if d.Decode(&fs) != nil || len(fs.Read)+len(fs.Write)+len(fs.Entries) == 0 {
				return nil, unsupported
			}
			for _, e := range fs.Entries {
				if e.Access != "read" && e.Access != "write" && e.Access != "deny" {
					return nil, unsupported
				}
				switch e.Path.Type {
				case "path":
					if e.Path.Path == "" || e.Path.Pattern != "" {
						return nil, unsupported
					}
				case "glob_pattern":
					if e.Path.Pattern == "" || e.Path.Path != "" {
						return nil, unsupported
					}
				default:
					return nil, unsupported
				}
			}
		}
		grants[name] = value
	}
	if len(grants) == 0 {
		return nil, unsupported
	}
	return response, nil
}
