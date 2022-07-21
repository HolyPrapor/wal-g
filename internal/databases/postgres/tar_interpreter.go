package postgres

import (
	"archive/tar"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"github.com/wal-g/tracelog"
	"github.com/wal-g/wal-g/internal"
	"github.com/wal-g/wal-g/utility"
	"golang.org/x/sys/unix"
)

var deprecatedFileSyncMessage = fmt.Sprintf(
	`You are using deprecated functionality that uses an inefficient fsync-for-every-file logic.
It will be removed in next major version.
Please use file sync mode using environment variable %s instead of using %s.
`, internal.TarFsyncMode, internal.TarDisableFsyncSetting)
var fallingBackFileSyncMessage = fmt.Sprintf(
	`Falling back to %s file sync mode.`,
	internal.DISABLED
)

// FileTarInterpreter extracts input to disk.
type FileTarInterpreter struct {
	DBDataDirectory string
	Sentinel        BackupSentinelDto
	FilesMetadata   FilesMetadataDto
	FilesToUnwrap   map[string]bool
	UnwrapResult    *UnwrapResult

	createNewIncrementalFiles bool
}

func NewFileTarInterpreter(
	dbDataDirectory string, sentinel BackupSentinelDto, filesMetadata FilesMetadataDto,
	filesToUnwrap map[string]bool, createNewIncrementalFiles bool,
) *FileTarInterpreter {
	return &FileTarInterpreter{dbDataDirectory, sentinel, filesMetadata,
		filesToUnwrap, newUnwrapResult(), createNewIncrementalFiles}
}

// write file from reader to local file
func WriteLocalFile(fileReader io.Reader, header *tar.Header, localFile *os.File, fsync bool) error {
	_, err := io.Copy(localFile, fileReader)
	if err != nil {
		err1 := os.Remove(localFile.Name())
		if err1 != nil {
			tracelog.ErrorLogger.Fatalf("Interpret: failed to remove localFile '%s' because of error: %v",
				localFile.Name(), err1)
		}
		return errors.Wrap(err, "Interpret: copy failed")
	}

	mode := os.FileMode(header.Mode)
	if err = localFile.Chmod(mode); err != nil {
		return errors.Wrap(err, "Interpret: chmod failed")
	}

	if fsync {
		err = localFile.Sync()
		return errors.Wrap(err, "Interpret: fsync failed")
	}

	return nil
}

// TODO : unit tests
func (tarInterpreter *FileTarInterpreter) unwrapRegularFileOld(fileReader io.Reader,
	fileInfo *tar.Header,
	targetPath string,
	fsync bool) error {
	if tarInterpreter.FilesToUnwrap != nil {
		if _, ok := tarInterpreter.FilesToUnwrap[fileInfo.Name]; !ok {
			// don't have to unwrap it this time
			tracelog.DebugLogger.Printf("Don't have to unwrap '%s' this time\n", fileInfo.Name)
			return nil
		}
	}
	fileDescription, haveFileDescription := tarInterpreter.FilesMetadata.Files[fileInfo.Name]

	// If this file is incremental we use it's base version from incremental path
	if haveFileDescription && tarInterpreter.Sentinel.IsIncremental() && fileDescription.IsIncremented {
		err := ApplyFileIncrement(targetPath, fileReader, tarInterpreter.createNewIncrementalFiles, fsync)
		return errors.Wrapf(err, "Interpret: failed to apply increment for '%s'", targetPath)
	}
	err := PrepareDirs(fileInfo.Name, targetPath)
	if err != nil {
		return errors.Wrap(err, "Interpret: failed to create all directories")
	}
	file, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return errors.Wrapf(err, "failed to create new file: '%s'", targetPath)
	}
	defer utility.LoggedClose(file, "")

	return WriteLocalFile(fileReader, fileInfo, file, fsync)
}

// Interpret extracts a tar file to disk and creates needed directories.
// Returns the first error encountered. Calls fsync after each file
// is written successfully.
func (tarInterpreter *FileTarInterpreter) Interpret(fileReader io.Reader, fileInfo *tar.Header) error {
	tracelog.DebugLogger.Println("Interpreting: ", fileInfo.Name)
	targetPath := path.Join(tarInterpreter.DBDataDirectory, fileInfo.Name)

	switch fileInfo.Typeflag {
	case tar.TypeReg, tar.TypeRegA:
		// temporary switch to determine if new unwrap logic should be used
		if useNewUnwrapImplementation {
			return tarInterpreter.unwrapRegularFileNew(fileReader, fileInfo, targetPath, false)
		}
		return tarInterpreter.unwrapRegularFileOld(fileReader, fileInfo, targetPath, false)
	case tar.TypeDir:
		err := os.MkdirAll(targetPath, 0755)
		if err != nil {
			return errors.Wrapf(err, "Interpret: failed to create all directories in %s", targetPath)
		}
		if err = os.Chmod(targetPath, os.FileMode(fileInfo.Mode)); err != nil {
			return errors.Wrap(err, "Interpret: chmod failed")
		}
	case tar.TypeLink:
		if err := os.Link(fileInfo.Name, targetPath); err != nil {
			return errors.Wrapf(err, "Interpret: failed to create hardlink %s", targetPath)
		}
	case tar.TypeSymlink:
		if err := os.Symlink(fileInfo.Name, targetPath); err != nil {
			return errors.Wrapf(err, "Interpret: failed to create symlink %s", targetPath)
		}
	}
	return nil
}

func (tarInterpreter *FileTarInterpreter) OnInterpretFinish() error {
	fileSyncMode := getFileSyncMode()
	if fileSyncMode == internal.GLOBAL || fileSyncMode == internal.DEFAULT {
		_, _, err := unix.Syscall(unix.SYS_SYNC, 0, 0, 0)
		if err != 0 {
			return errors.Errorf("FileTarInterpreter: global fsync failed with error code %d", err)
		}
	}
	return nil
}

// PrepareDirs makes sure all dirs exist
func PrepareDirs(fileName string, targetPath string) error {
	if fileName == targetPath {
		return nil // because it runs in the local directory
	}
	base := filepath.Base(fileName)
	dir := strings.TrimSuffix(targetPath, base)
	err := os.MkdirAll(dir, 0755)
	return err
}

func getFileSyncMode() internal.TarFsyncMode {
	var fsyncMode internal.WalFsyncMode = viper.GetString(internal.TarFsyncMode)

	if viper.IsSet(internal.TarDisableFsyncSetting) {
		tracelog.WarningLogger.Printf(deprecatedFileSyncMessage)
		if fsyncMode == internal.DEFAULT && viper.GetBool(internal.TarDisableFsyncSetting) {
			tracelog.WarningLogger.Printf(fallingBackFileSyncMessage)
			fsyncMode = internal.DISABLED
		}
	}

	return fsyncMode
}