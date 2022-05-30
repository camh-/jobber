package job

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type DiskIOLimits struct {
	Device    string
	Major     uint32
	Minor     uint32
	ReadBPS   uint64
	WriteBPS  uint64
	ReadIOPS  uint32
	WriteIOPS uint32
}

// UnmarshalText unmarshals a string ([]byte) into a DiskIOLimits. It is used
// by kong to unmarshal the command line argument into a structured value.
//
// The format of the input string is 5 or 6 colon separated values. If 5 values,
// the first should be a block device filesystem path that can be stat'ed to
// get its major and minor number. If 6 values, they are directly the major and
// minor number.
//
// The remaining 4 values are the disk IO limits for that block device. A field
// may be empty which is parsed as zero, which means no setting for that throttle.
func (d *DiskIOLimits) UnmarshalText(b []byte) (err error) {
	parseVal := func(s string, bits int, name string, optional bool) (v uint64) {
		if err != nil {
			return 0
		}
		if optional && s == "" {
			return 0
		}
		v, err = strconv.ParseUint(s, 10, bits)
		if err != nil {
			err = fmt.Errorf("could not parse %s %s: %w", name, s, err)
		}
		return v
	}

	parts := strings.Split(string(b), ":")
	switch len(parts) {
	case 5:
		d.Device = parts[0]
		parts = parts[1:]
	case 6:
		d.Major = uint32(parseVal(parts[0], 32, "major", false /* optional */))
		d.Minor = uint32(parseVal(parts[1], 32, "minor", false /* optional */))
		parts = parts[2:]
	default:
		return errors.New("wrong number of fields")
	}

	d.ReadBPS = parseVal(parts[0], 64, "readBPS", true /* optional */)
	d.WriteBPS = parseVal(parts[1], 64, "writeBPS", true /* optional */)
	d.ReadIOPS = uint32(parseVal(parts[2], 32, "readIOPS", true /* optional */))
	d.WriteIOPS = uint32(parseVal(parts[3], 32, "writeIOPS", true /* optional */))

	return err
}

func (d *DiskIOLimits) String() string {
	return fmt.Sprintf("%d:%d:%d:%d:%d:%d", d.Major, d.Minor, d.ReadBPS, d.WriteBPS, d.ReadIOPS, d.WriteIOPS)
}

func (d *DiskIOLimits) cgval() string {
	// a zero value for a limit is not written out. Alternatively it could mean
	// "max", but is makes little difference as we do not have a job hierarchy, so
	// all start at max and can be overridden to lower values. To leave at max,
	// set to 0.
	vals := []string{}
	if d.ReadBPS != 0 {
		vals = append(vals, fmt.Sprintf("rbps=%d", d.ReadBPS))
	}
	if d.WriteBPS != 0 {
		vals = append(vals, fmt.Sprintf("wbps=%d", d.WriteBPS))
	}
	if d.ReadIOPS != 0 {
		vals = append(vals, fmt.Sprintf("riops=%d", d.ReadIOPS))
	}
	if d.WriteIOPS != 0 {
		vals = append(vals, fmt.Sprintf("wiops=%d", d.WriteIOPS))
	}
	// a major:minor with no parameters is valid and ignored by the kernel
	return fmt.Sprintf("%d:%d %s", d.Major, d.Minor, strings.Join(vals, " "))
}

func (d *DiskIOLimits) ResolveDevice() error {
	if d.Device == "" {
		return fmt.Errorf("device not set")
	}
	var stat syscall.Stat_t
	if err := syscall.Stat(d.Device, &stat); err != nil {
		return fmt.Errorf("could not stat: %s: %w", d.Device, err)
	}
	if (stat.Mode & unix.S_IFMT) != unix.S_IFBLK {
		return fmt.Errorf("not a block device: %s", d.Device)
	}
	d.Major = unix.Major(stat.Dev)
	d.Minor = unix.Minor(stat.Dev)

	return nil
}
