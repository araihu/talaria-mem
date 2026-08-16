package projection

import "errors"

type FailpointName string

const (
	FailpointBeforeTemp          FailpointName = "create_temp"
	FailpointBeforeFileSync      FailpointName = "file_fsync"
	FailpointBeforeRename        FailpointName = "rename"
	FailpointBeforeDirectorySync FailpointName = "directory_fsync"
	FailpointBeforeVerify        FailpointName = "verification"
	FailpointBeforeAcknowledge   FailpointName = "acknowledgement"
)

type Failpoint interface {
	Hit(name FailpointName) error
}

type StaticFailpoint struct {
	Name FailpointName
	Err  error
}

func (point StaticFailpoint) Hit(name FailpointName) error {
	if point.Name == name {
		if point.Err != nil {
			return point.Err
		}
		return errors.New("projection failpoint")
	}
	return nil
}
