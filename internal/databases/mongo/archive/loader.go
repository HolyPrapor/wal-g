package archive

import (
	"fmt"
	"io"
	"time"

	"github.com/wal-g/wal-g/internal"
	"github.com/wal-g/wal-g/internal/databases/mongo/models"
	"github.com/wal-g/wal-g/utility"

	"github.com/wal-g/storages/storage"
)

// StreamSentinelDto represents backup sentinel data
type StreamSentinelDto struct {
	StartLocalTime  time.Time   `json:"StartLocalTime,omitempty"`
	FinishLocalTime time.Time   `json:"FinishLocalTime,omitempty"`
	UserData        interface{} `json:"UserData,omitempty"`
}

// Uploader defines interface to store mongodb backups and oplog archives
type Uploader interface {
	UploadOplogArchive(stream io.Reader, arch models.Archive) error
	UploadBackup(stream io.Reader) error
	FileExtension() string
}

// Downloader defines interface to fetch mongodb oplog archives
type Downloader interface {
	DownloadOplogArchive(arch models.Archive, writeCloser io.WriteCloser) error
	ListOplogArchives() ([]models.Archive, error)
}

// StorageDownloader extends base folder with mongodb specific.
type StorageDownloader struct {
	folder storage.Folder
}

// NewStorageDownloader builds mongodb downloader.
func NewStorageDownloader(path string) (*StorageDownloader, error) {
	folder, err := internal.ConfigureFolder()
	if err != nil {
		return nil, err
	}
	folder = folder.GetSubFolder(path)
	return &StorageDownloader{folder}, nil
}

// DownloadOplogArchive downloads, decompresses and decrypts (if needed) oplog archive.
func (sd *StorageDownloader) DownloadOplogArchive(arch models.Archive, writeCloser io.WriteCloser) error {
	return internal.DownloadFile(sd.folder, arch.Filename(), arch.Extension(), writeCloser)
}

// ListOplogArchives fetches all oplog archives existed in storage.
func (sd *StorageDownloader) ListOplogArchives() ([]models.Archive, error) {
	objects, _, err := sd.folder.ListFolder()
	if err != nil {
		return nil, fmt.Errorf("can not list archive folder: %w", err)
	}

	archives := make([]models.Archive, 0, len(objects))
	for _, key := range objects {
		archName := key.GetName()
		arch, err := models.ArchFromFilename(archName)
		if err != nil {
			return nil, fmt.Errorf("can not convert retrieve timestamps from oplog archive Ext '%s': %w", archName, err)
		}
		archives = append(archives, arch)
	}
	return archives, nil
}

// StorageUploader extends base uploader with mongodb specific.
type StorageUploader struct {
	*internal.Uploader
}

// NewStorageUploader builds mongodb uploader.
func NewStorageUploader(path string) (*StorageUploader, error) {
	uploader, err := internal.ConfigureUploader()
	if err != nil {
		return nil, err
	}
	if path != "" {
		uploader.UploadingFolder = uploader.UploadingFolder.GetSubFolder(path)
	}
	return &StorageUploader{uploader}, nil
}

// UploadOplogArchive compresses a stream and uploads it with given archive name.
func (su *StorageUploader) UploadOplogArchive(stream io.Reader, arch models.Archive) error {
	//if err := sa.uploader.UploadOplogArchive(&buf, arch.Filename()); err != nil {

	if err := su.PushStreamToDestination(stream, arch.Filename()); err != nil {
		return fmt.Errorf("error while uploading stream: %w", err)
	}
	return nil
}

// UploadBackup compresses a stream and uploads it.
func (su *StorageUploader) UploadBackup(stream io.Reader) error {
	timeStart := utility.TimeNowCrossPlatformLocal()
	backupName, err := su.PushStream(stream)
	if err != nil {
		return err
	}
	currentBackupSentinelDto := &StreamSentinelDto{
		StartLocalTime:  timeStart,
		FinishLocalTime: utility.TimeNowCrossPlatformLocal(),
		UserData:        internal.GetSentinelUserData(),
	}
	return internal.UploadSentinel(su.Uploader, currentBackupSentinelDto, backupName)
}

// FileExtension returns current file extension (based on configured compression)
func (su *StorageUploader) FileExtension() string {
	return su.Compressor.FileExtension()
}
