// Package acc provides an interface to the Autodesk Construction Cloud
package acc

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/rclone/rclone/backend/autodesk/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/hash"
	"golang.org/x/time/rate"
)

// Register with Fs
func init() {
	fs.Register(&fs.RegInfo{
		Name:        "acc",
		Description: "Autodesk Construction Cloud",
		NewFs:       NewFs,
		Options: []fs.Option{
			{
				Name:     "token",
				Help:     "OAuth token as JSON blob",
				Required: true,
			},
			{
				Name:     "hub_id",
				Help:     "The unique identifier of a hub",
				Required: true,
			},
			{
				Name:     "project_id",
				Help:     "The unique identifier of a project. For BIM 360 Docs, add 'b.' prefix to the BIM 360 API project ID",
				Required: true,
			},
			{
				Name:     "client_id",
				Help:     "OAuth Client ID",
				Required: true,
			},
			{
				Name:     "client_secret",
				Help:     "OAuth Client Secret",
				Required: true,
			},
			// Add more options as needed
		},
	})
}

// NewFs constructs an Fs from the path, container:path
func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	// Parse config into Options struct
	opt := new(Options)
	err := configstruct.Set(m, opt)
	if err != nil {
		return nil, err
	}
	// Parse the token JSON
	var tokenInfo TokenInfo
	if opt.Token != "" {
		err = json.Unmarshal([]byte(opt.Token), &tokenInfo)
		if err != nil {
			return nil, fmt.Errorf("failed to parse token JSON: %w", err)
		}
	} else {
		return nil, fmt.Errorf("token is required")
	}

	// Default root to "Project Files" if empty or "/"
	if root == "" || root == "/" {
		root = "/Project Files"
	}

	// Create a new file system
	f := &Fs{
		name:        name,
		root:        root,
		opt:         *opt,
		tokenInfo:   tokenInfo,
		tokenExpiry: tokenInfo.Expiry,
		rateLimiter: rate.NewLimiter(rate.Limit(300.0/60.0), 1), // 300 requests per minute
	}

	// Initialize features
	f.features = &fs.Features{
		CanHaveEmptyDirectories: true,
	}

	// Remove leading and trailing slashes from root
	f.root = strings.Trim(f.root, "/")
	// and add a leading slash
	f.root = "/" + f.root

	// Check if the root exists
	if f.root != "/" {
		// Get the folder structure
		folderStructure, err := f.GetFullFolderStructure(ctx)
		if err != nil {
			return nil, fmt.Errorf("error getting folder structure: %w", err)
		}
		f.folderStructure = folderStructure

		// Check if the root path exists in the folder structure
		rootExists := false
		for _, namePath := range folderStructure {
			if namePath == f.root {
				rootExists = true
				break
			}
		}

		if !rootExists {
			return nil, fmt.Errorf("root path not found: %s", f.root)
		}
	}

	fs.Debugf(f, "Created new file system for %s with root %s", name, f.root)
	return f, nil
}

// Name of the remote (as passed into NewFs)
func (f *Fs) Name() string {
	return f.name
}

// Root of the remote (as passed into NewFs)
func (f *Fs) Root() string {
	return f.root
}

// String converts this Fs to a string
func (f *Fs) String() string {
	return fmt.Sprintf("ACC root '%s'", f.root)
}

// Features returns the optional features of this Fs
func (f *Fs) Features() *fs.Features {
	return f.features
}

// Precision of the ModTimes in this Fs
func (f *Fs) Precision() time.Duration {
	return time.Second
}

// Hashes returns the supported hash types of the filesystem
func (f *Fs) Hashes() hash.Set {
	return hash.Set(hash.SHA1)
}

// List the objects and directories in dir into entries
func (f *Fs) List(ctx context.Context, dir string) (entries fs.DirEntries, err error) {
	fs.Debugf(f, "List: Starting for directory: %s", dir)

	// Construct the full path by joining the root and dir
	fullPath := path.Join(f.root, dir)
	fs.Debugf(f, "List: Full path: %s", fullPath)

	if fullPath == "/" {
		fs.Debugf(f, "List: Listing top folders")
		return f.listTopFolders(ctx)
	}

	// Get the folder ID for the given path
	folderID, err := f.GetFolderIDByPath(ctx, fullPath)
	if err != nil {
		fs.Debugf(f, "List: Error getting folder ID for path '%s': %v", fullPath, err)
		return nil, err
	}
	fs.Debugf(f, "List: Got folder ID: %s for path: %s", folderID, fullPath)

	return f.listFolderContents(ctx, folderID, dir)
}

// listTopFolders lists the contents of all top folders
func (f *Fs) listTopFolders(ctx context.Context) (entries fs.DirEntries, err error) {
	fs.Debugf(f, "listTopFolders: Starting")

	// If root is not empty, we should only list folders under the root
	if f.root != "/" {
		// Get the folder ID for the root path
		folderID, err := f.GetFolderIDByPath(ctx, f.root)
		if err != nil {
			fs.Debugf(f, "listTopFolders: Error getting folder ID for root path '%s': %v", f.root, err)
			return nil, fmt.Errorf("error getting folder ID for root path '%s': %w", f.root, err)
		}

		// List the contents of the root folder instead of all top folders
		return f.listFolderContents(ctx, folderID, "")
	}

	// Original code for listing all top folders when root is "/"
	// ... existing code ...

	// Ensure we have the folder structure
	if f.folderStructure == nil {
		var err error
		f.folderStructure, err = f.GetFullFolderStructure(ctx)
		if err != nil {
			fs.Debugf(f, "listTopFolders: Error getting full folder structure: %v", err)
			return nil, fmt.Errorf("error getting full folder structure: %w", err)
		}
	}

	// Map to store unique top folders
	topFolders := make(map[string]string)

	// Iterate through the folder structure to find top-level folders
	for idPath, namePath := range f.folderStructure {
		parts := strings.Split(strings.Trim(idPath, "/"), "/")
		if len(parts) == 1 {
			// Extract just the folder name without any path
			nameOnly := path.Base(strings.Trim(namePath, "/"))
			topFolders[parts[0]] = nameOnly
		}
	}

	fs.Debugf(f, "listTopFolders: Found %d top folders", len(topFolders))

	for folderID, folderName := range topFolders {
		fs.Debugf(f, "listTopFolders: Processing top folder: %s (ID: %s)", folderName, folderID)

		// Add the top folder itself - using just the folder name, not the full path
		d := fs.NewDir(folderName, time.Time{}) // We don't have the modification time here
		entries = append(entries, d)
	}

	fs.Debugf(f, "listTopFolders: Completed. Returning %d entries", len(entries))
	return entries, nil
}

// listFolderContents lists the contents of a specific folder
func (f *Fs) listFolderContents(ctx context.Context, folderID, prefix string) (entries fs.DirEntries, err error) {
	fs.Debugf(f, "listFolderContents: Starting for folder ID: %s, prefix: %s", folderID, prefix)

	opts := FolderContentsOptions{
		FilterType:    []string{"folders", "items"},
		IncludeHidden: false,
	}

	folders, versions, err := f.GetAllFolderContents(ctx, folderID, opts)
	if err != nil {
		fs.Debugf(f, "listFolderContents: Error getting all folder contents: %v", err)
		return nil, err
	}
	fs.Debugf(f, "listFolderContents: Retrieved %d folders and %d versions", len(folders), len(versions))

	// Process folders
	for _, folder := range folders {
		remote := path.Join(prefix, folder.Attributes.Name)
		d := fs.NewDir(remote, time.Time(folder.Attributes.LastModifiedTime))
		entries = append(entries, d)
		fs.Debugf(f, "listFolderContents: Added folder: %s", remote)
	}

	// Process versions
	for _, version := range versions {
		remote := path.Join(prefix, version.Data.Attributes.Name)
		fs.Debugf(f, "listFolderContents: Processing version: %s", remote)

		// Debug logging for storageUrn
		storageUrn := version.Data.Relationships.Storage.Data.ID
		fs.Debugf(f, "  Storage URN: %s", storageUrn)

		bucket, objectKey, err := f.parseBucketAndObjectKeyFromURN(storageUrn)
		if err != nil {
			fs.Debugf(f, "listFolderContents: Error parsing bucket and object key for '%s': %v", remote, err)
			continue
		}
		fs.Debugf(f, "listFolderContents: Parsed bucket: %s, object key: %s", bucket, objectKey)

		objectDetails, err := f.GetObjectDetails(ctx, bucket, objectKey)
		if err != nil {
			fs.Debugf(f, "listFolderContents: Error getting object details for '%s': %v", remote, err)
			continue
		}
		fs.Debugf(f, "listFolderContents: Got object details for '%s': size=%d, SHA1=%s", remote, objectDetails.Size, objectDetails.SHA1)

		o := &Object{
			fs:            f,
			remote:        remote,
			size:          objectDetails.Size,
			modTime:       version.Data.Attributes.LastModifiedTime,
			id:            version.Data.ID,
			itemID:        version.Data.Relationships.Item.Data.ID,
			storageUrn:    storageUrn,
			objectDetails: objectDetails,
		}
		entries = append(entries, o)
		fs.Debugf(f, "listFolderContents: Added object: %s", remote)
	}

	fs.Debugf(f, "listFolderContents: Completed. Returning %d entries", len(entries))
	return entries, nil
}

// NewObject finds the Object at remote
func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	fs.Debugf(f, "NewObject: Starting for remote: %s", remote)

	// Construct the full path by joining the root and remote
	fullPath := path.Join(f.root, remote)
	fs.Debugf(f, "NewObject: Full path: %s", fullPath)

	itemID, err := f.GetItemIDByPath(ctx, fullPath)
	if err != nil {
		fs.Debugf(f, "NewObject: Error getting item ID for path '%s': %v", fullPath, err)
		return nil, fmt.Errorf("couldn't find object '%s': %w", remote, err)
	}
	fs.Debugf(f, "NewObject: Got item ID: %s for path: %s", itemID, fullPath)

	version, err := f.GetItemByID(ctx, itemID)
	if err != nil {
		fs.Debugf(f, "NewObject: Error getting item details for ID '%s': %v", itemID, err)
		return nil, fmt.Errorf("couldn't get item details for '%s': %w", remote, err)
	}
	fs.Debugf(f, "NewObject: Got item details for ID: %s", itemID)

	storageUrn := ""
	if version.Data.Attributes.Extension.Data.StorageUrn != "" {
		storageUrn = version.Data.Attributes.Extension.Data.StorageUrn
		fs.Debugf(f, "NewObject: Using storage URN from version data: %s", storageUrn)
	} else {
		fs.Debugf(f, "NewObject: No storage URN found in version data")
	}

	bucket, objectKey, err := f.parseBucketAndObjectKeyFromURN(storageUrn)
	if err != nil {
		fs.Debugf(f, "NewObject: Error parsing bucket and object key from URN '%s': %v", storageUrn, err)
		return nil, fmt.Errorf("couldn't parse bucket and object key for '%s': %w", remote, err)
	}
	fs.Debugf(f, "NewObject: Parsed bucket: %s, object key: %s", bucket, objectKey)

	objectDetails, err := f.GetObjectDetails(ctx, bucket, objectKey)
	if err != nil {
		fs.Debugf(f, "NewObject: Error getting object details for bucket '%s' and key '%s': %v", bucket, objectKey, err)
		return nil, fmt.Errorf("couldn't get object details for '%s': %w", remote, err)
	}
	fs.Debugf(f, "NewObject: Got object details. Size: %d, SHA1: %s", objectDetails.Size, objectDetails.SHA1)

	obj := &Object{
		fs:            f,
		remote:        remote,
		size:          objectDetails.Size,
		modTime:       version.Data.Attributes.LastModifiedTime,
		id:            version.Data.ID,
		itemID:        itemID,
		storageUrn:    storageUrn,
		objectDetails: objectDetails,
	}

	fs.Debugf(f, "NewObject: Created Object for remote '%s'. Size: %d, ModTime: %v, ID: %s",
		obj.remote, obj.size, obj.modTime, obj.id)

	return obj, nil
}

// Put uploads data to the remote path
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	remotePath := src.Remote()
	// Construct the full path by joining the root and remotePath
	fullPath := path.Join(f.root, remotePath)
	remoteDir, fileName := path.Split(fullPath)
	remoteDir = strings.TrimSuffix(remoteDir, "/")

	fs.Debugf(f, "Put: Starting for remote path: %s (full path: %s, dir: %s, file: %s)",
		remotePath, fullPath, remoteDir, fileName)

	// Try to get the folder ID for the remote directory
	folderID, err := f.GetFolderIDByPath(ctx, remoteDir)
	if err != nil {
		fs.Debugf(f, "Put: Folder not found: %s, attempting to create it", remoteDir)

		// Create the directory structure
		err = f.Mkdir(ctx, path.Dir(remotePath))
		if err != nil {
			return nil, fmt.Errorf("put: error creating directory structure: %w", err)
		}

		// Now try to get the folder ID again
		folderID, err = f.GetFolderIDByPath(ctx, remoteDir)
		if err != nil {
			return nil, fmt.Errorf("put: error getting folder ID after creation: %w", err)
		}
	}

	fs.Debugf(f, "Put: Got folder ID: %s for path: %s", folderID, remoteDir)

	// Upload the file directly from the reader
	obj, err := f.UploadFile(ctx, folderID, fileName, in, src.Size())
	if err != nil {
		return nil, fmt.Errorf("error uploading file: %w", err)
	}

	fs.Debugf(f, "Put: Successfully uploaded file: %s", fileName)

	return obj, nil
}

// Mkdir makes the directory (container, bucket) recursively
func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	fs.Debugf(f, "Mkdir: Starting for directory: %s (root: %s)", dir, f.root)

	// If dir is empty or root, nothing to do
	if dir == "" || dir == "/" {
		fs.Debugf(f, "Mkdir: Nothing to do for empty or root directory")
		return nil
	}

	// Normalize the paths
	rootPath := strings.Trim(path.Clean("/"+f.root), "/")
	dirPath := strings.Trim(path.Clean("/"+dir), "/")

	fs.Debugf(f, "Mkdir: Normalized paths - root: %s, dir: %s", rootPath, dirPath)

	// Split paths into components
	rootParts := strings.Split(rootPath, "/")
	dirParts := strings.Split(dirPath, "/")

	// Check if we're trying to create a folder that would duplicate part of the root path
	// For example, if root is "/Project Files" and dir is "Project Files/blah3"
	if len(dirParts) > 0 && len(rootParts) > 0 {
		// If the first part of dir matches any part of root, we need to adjust
		if dirParts[0] == rootParts[len(rootParts)-1] {
			// Remove the duplicate part from dirParts
			dirParts = dirParts[1:]
			fs.Debugf(f, "Mkdir: Removed duplicate path segment, new dir parts: %v", dirParts)
		}
	}

	// If no parts left after adjustment, nothing to do
	if len(dirParts) == 0 {
		fs.Debugf(f, "Mkdir: No directories to create after adjustment")
		return nil
	}

	// Get the folder ID for the root path
	parentFolderID, err := f.GetFolderIDByPath(ctx, f.root)
	if err != nil {
		fs.Debugf(f, "Mkdir: Error getting folder ID for root path '%s', trying to get top folders", f.root)

		// If we can't get the folder ID for the root path, try to get the top folders
		topFolders, err := f.GetTopFolders(ctx)
		if err != nil {
			return fmt.Errorf("mkdir: error getting top folders: %w", err)
		}

		if len(topFolders) == 0 {
			return fmt.Errorf("mkdir: no top folders found in project")
		}

		// Try to find a top folder that matches the first part of our root path
		found := false
		for _, folder := range topFolders {
			if folder.Attributes.Name == rootParts[0] {
				parentFolderID = folder.ID
				found = true
				fs.Debugf(f, "Mkdir: Found matching top folder: %s (ID: %s)",
					folder.Attributes.Name, parentFolderID)
				break
			}
		}

		if !found {
			// Use the first top folder as a fallback
			parentFolderID = topFolders[0].ID
			fs.Debugf(f, "Mkdir: Using first top folder as fallback: %s (ID: %s)",
				topFolders[0].Attributes.Name, parentFolderID)
		}
	}

	fs.Debugf(f, "Mkdir: Got parent folder ID: %s for path: %s", parentFolderID, f.root)

	// Create each directory in the path
	currentPath := f.root
	for _, part := range dirParts {
		if part == "" {
			continue
		}

		currentPath = path.Join(currentPath, part)

		// Check if the folder already exists
		existingFolderID, err := f.GetFolderIDByPath(ctx, currentPath)
		if err == nil {
			// Folder exists, use it as the parent for the next iteration
			parentFolderID = existingFolderID
			fs.Debugf(f, "Mkdir: Folder already exists: %s (ID: %s)", currentPath, parentFolderID)
			continue
		}

		fs.Debugf(f, "Mkdir: Creating folder: %s with parent ID: %s", part, parentFolderID)
		folder, err := f.CreateFolder(ctx, parentFolderID, part)
		if err != nil {
			// Check if the error is because the folder already exists
			if strings.Contains(err.Error(), "FOLDER_ALREADY_EXIST") {
				fs.Debugf(f, "Mkdir: Folder already exists (from API error): %s", part)

				// Try to get the folder ID again after a short delay
				time.Sleep(500 * time.Millisecond)
				existingFolderID, getErr := f.GetFolderIDByPath(ctx, currentPath)
				if getErr == nil {
					// Successfully got the folder ID, use it as parent for next iteration
					parentFolderID = existingFolderID
					fs.Debugf(f, "Mkdir: Retrieved existing folder ID: %s for path: %s", parentFolderID, currentPath)
					continue
				}

				// If we still can't get the folder ID, refresh the folder structure and try again
				fs.Debugf(f, "Mkdir: Refreshing folder structure to find existing folder")
				newFolderStructure, refreshErr := f.GetFullFolderStructure(ctx)
				if refreshErr == nil {
					f.folderStructure = newFolderStructure
					existingFolderID, getErr = f.GetFolderIDByPath(ctx, currentPath)
					if getErr == nil {
						parentFolderID = existingFolderID
						fs.Debugf(f, "Mkdir: Found folder after refresh: %s (ID: %s)", currentPath, parentFolderID)
						continue
					}
				}
			}

			return fmt.Errorf("error creating folder %s: %w", part, err)
		}

		// Update folder structure with new folder
		if f.folderStructure != nil {
			idPath := path.Join("/", parentFolderID, folder.ID)
			namePath := currentPath
			f.folderStructure[idPath] = namePath
			fs.Debugf(f, "Mkdir: Updated folder structure with new folder: ID Path: %s, Name Path: %s",
				idPath, namePath)
		}

		parentFolderID = folder.ID
		fs.Debugf(f, "Mkdir: Created folder: %s (ID: %s)", currentPath, parentFolderID)
	}

	// Refresh the folder structure after creating new folders
	newFolderStructure, err := f.GetFullFolderStructure(ctx)
	if err != nil {
		fs.Debugf(f, "Mkdir: Error refreshing folder structure: %v", err)
		// Not returning an error here as the folders were created successfully
	} else {
		f.folderStructure = newFolderStructure
		fs.Debugf(f, "Mkdir: Successfully refreshed folder structure")
	}

	return nil
}

// Rmdir removes the directory (container, bucket) by setting it to hidden
func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	fs.Debugf(f, "Rmdir: Starting for directory: %s", dir)

	// Construct the full path by joining the root and dir
	fullPath := path.Join(f.root, dir)
	fs.Debugf(f, "Rmdir: Full path: %s", fullPath)

	// Get the folder ID for the given path
	folderID, err := f.GetFolderIDByPath(ctx, fullPath)
	if err != nil {
		// If the folder is not found, it might already be deleted
		// or never existed, so we can consider this a success
		if strings.Contains(err.Error(), "folder not found") {
			fs.Debugf(f, "Rmdir: Folder not found, considering already removed: %s", fullPath)
			return nil
		}
		return fmt.Errorf("rmdir: error getting folder ID: %w", err)
	}

	// Prepare the request payload
	payload := map[string]interface{}{
		"jsonapi": map[string]string{
			"version": "1.0",
		},
		"data": map[string]interface{}{
			"type": "folders",
			"id":   folderID,
			"attributes": map[string]interface{}{
				"hidden": true,
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error marshaling payload: %w", err)
	}

	// Prepare the request URL
	url := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/folders/%s", f.opt.ProjectID, folderID)

	// Send the request
	resp, err := f.doRequest(ctx, "PATCH", url, bytes.NewBuffer(jsonPayload), map[string]string{
		"Content-Type": "application/vnd.api+json",
	})
	if err != nil {
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	// Check the response
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API request failed with status code: %d, body: %s", resp.StatusCode, string(body))
	}

	// Update the folder structure to reflect the change
	if f.folderStructure != nil {
		// Find and remove the folder and its children from the structure
		keysToRemove := []string{}
		for idPath, namePath := range f.folderStructure {
			if strings.Contains(idPath, "/"+folderID) || strings.HasSuffix(namePath, fullPath) {
				keysToRemove = append(keysToRemove, idPath)
			}
		}

		for _, key := range keysToRemove {
			delete(f.folderStructure, key)
		}

		fs.Debugf(f, "Rmdir: Removed %d entries from folder structure", len(keysToRemove))
	}

	fs.Debugf(f, "Rmdir: Successfully marked folder as hidden: %s", dir)
	return nil
}

// Upload uploads a file from a local path to ACC
func (f *Fs) Upload(ctx context.Context, localPath, remotePath string) (fs.Object, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("error opening local file: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("error getting file info: %w", err)
	}

	remoteDir, fileName := path.Split(remotePath)
	folderID, err := f.GetFolderIDByPath(ctx, remoteDir)
	if err != nil {
		return nil, fmt.Errorf("upload: error getting folder ID: %w", err)
	}

	obj, err := f.UploadFile(ctx, folderID, fileName, file, fileInfo.Size())
	if err != nil {
		return nil, fmt.Errorf("error uploading file: %w", err)
	}

	return obj, nil
}

// Implement other necessary methods and types (e.g., Object, Directory) here

// Options defines the configuration for this backend
type Options struct {
	Token        string `config:"token"`
	HubID        string `config:"hub_id"`
	ProjectID    string `config:"project_id"`
	ClientID     string `config:"client_id"`
	ClientSecret string `config:"client_secret"`
	// Add more fields as needed
}

// TokenInfo represents the parsed token information
type TokenInfo struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
}

// Fs represents a remote ACC filesystem
type Fs struct {
	name            string
	root            string
	opt             Options
	tokenInfo       TokenInfo
	features        *fs.Features
	tokenExpiry     time.Time
	rateLimiter     *rate.Limiter
	folderStructure map[string]string // New field to store the folder structure
	// Add more fields as needed
}

// Object describes an ACC object
type Object struct {
	fs            *Fs
	remote        string
	size          int64
	modTime       time.Time
	id            string
	itemID        string
	storageUrn    string
	objectDetails *api.ObjectDetails
}

// Implement fs.DirEntry interface for Object
func (o *Object) String() string {
	return o.remote
}

// Dir represents a directory in ACC
type Dir struct {
	fs     *Fs
	remote string
	id     string
}

// Implement fs.DirEntry interface for Dir
func (d *Dir) String() string {
	return d.remote
}

func (o *Object) Remote() string {
	return o.remote
}

func (o *Object) ModTime(ctx context.Context) time.Time {
	return o.modTime
}

func (o *Object) Size() int64 {
	return o.size
}

func (o *Object) Fs() fs.Info {
	return o.fs
}

func (o *Object) Hash(ctx context.Context, t hash.Type) (string, error) {
	if t != hash.SHA1 {
		return "", hash.ErrUnsupported
	}

	// If we don't have object details, return an error
	if o.objectDetails == nil {
		return "", fmt.Errorf("object details not available")
	}

	// Return the SHA1 hash from object details
	return o.objectDetails.SHA1, nil
}

func (o *Object) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	const maxRetries = 1
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		bucket, objectKey, err := o.fs.parseBucketAndObjectKeyFromURN(o.storageUrn)
		if err != nil {
			return nil, fmt.Errorf("error parsing bucket and object key: %w", err)
		}

		signedURLResp, err := o.fs.CreateSignedS3DownloadLink(ctx, bucket, objectKey)
		if err != nil {
			lastErr = fmt.Errorf("error creating signed S3 download link: %w", err)
			continue
		}

		req, err := http.NewRequestWithContext(ctx, "GET", signedURLResp.URL, nil)
		if err != nil {
			lastErr = fmt.Errorf("error creating request for signed URL: %w", err)
			continue
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("error downloading file: %w", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("unexpected status code when downloading: %d", resp.StatusCode)
			continue
		}

		// Validate that the signed URL response size matches our object size
		if o.size != signedURLResp.Size {
			fs.Errorf(o, "ACC API size inconsistency detected for '%s':", o.remote)
			fs.Errorf(o, "  Object details API returned size: %d bytes", o.size)
			fs.Errorf(o, "  Signed download API returned size: %d bytes", signedURLResp.Size)
			fs.Errorf(o, "  Difference: %d bytes (%.1f%%)", signedURLResp.Size-o.size, float64(signedURLResp.Size-o.size)/float64(o.size)*100)
			fs.Errorf(o, "This indicates an ACC API inconsistency. File transfers may fail due to size verification.")
			// We maintain the original object size to ensure rclone's corruption detection works correctly
		}

		// Validate SHA1 hash if available
		sha1Hash, _ := o.Hash(ctx, hash.SHA1)
		if sha1Hash != "" && signedURLResp.SHA1 != "" && sha1Hash != signedURLResp.SHA1 {
			fs.Errorf(o, "SHA1 mismatch detected: object metadata SHA1 %s vs actual file SHA1 %s", sha1Hash, signedURLResp.SHA1)
			// This indicates a potential data integrity issue
		}

		return resp.Body, nil
	}

	return nil, fmt.Errorf("failed to open object after %d attempts: %w", maxRetries, lastErr)
}

// Remove an object
func (o *Object) Remove(ctx context.Context) error {
	fs.Debugf(o, "Remove: Starting for object %s (ID: %s)", o.remote, o.id)

	// Parse URN and version number
	parts := strings.Split(o.id, "?version=")
	if len(parts) != 2 {
		return fmt.Errorf("invalid version ID format: %s", o.id)
	}
	urn, version := parts[0], parts[1]
	fs.Debugf(o, "Remove: Parsed URN: %s, Version: %s", urn, version)

	urlStr := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/versions", o.fs.opt.ProjectID)
	fs.Debugf(o, "Remove: POST request to %s", urlStr)

	payload := map[string]interface{}{
		"jsonapi": map[string]string{
			"version": "1.0",
		},
		"data": map[string]interface{}{
			"type": "versions",
			"attributes": map[string]interface{}{
				"extension": map[string]interface{}{
					"type":    "versions:autodesk.core:Deleted",
					"version": "1.0",
				},
			},
			"relationships": map[string]interface{}{
				"item": map[string]interface{}{
					"data": map[string]interface{}{
						"type": "items",
						"id":   o.itemID,
					},
				},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		fs.Debugf(o, "Remove: Error marshaling payload: %v", err)
		return fmt.Errorf("error marshaling payload: %w", err)
	}

	fs.Debugf(o, "Remove: Payload: %s", string(jsonPayload))

	resp, err := o.fs.doRequest(ctx, "POST", urlStr, bytes.NewBuffer(jsonPayload), map[string]string{
		"Content-Type": "application/vnd.api+json",
	})
	if err != nil {
		fs.Debugf(o, "Remove: Error making POST request: %v", err)
		return fmt.Errorf("error making POST request: %w", err)
	}
	defer resp.Body.Close()

	fs.Debugf(o, "Remove: Response status: %s", resp.Status)
	fs.Debugf(o, "Remove: Response headers: %+v", resp.Header)

	body, _ := io.ReadAll(resp.Body)
	fs.Debugf(o, "Remove: Response body: %s", string(body))

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status code when deleting object: %d, body: %s", resp.StatusCode, string(body))
	}

	fs.Debugf(o, "Remove: Successfully deleted object %s", o.remote)
	return nil
}

// SetModTime sets the modification time of the Object
func (o *Object) SetModTime(ctx context.Context, t time.Time) error {
	return fs.ErrorCantSetModTime
	/*
		fs.Debugf(o, "SetModTime: Starting for object %s with time %v", o.remote, t)

		encodedID := url.PathEscape(o.id)
		url := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/versions/%s?version=1", o.fs.opt.ProjectID, encodedID)

		payload := map[string]interface{}{
			"jsonapi": map[string]string{
				"version": "1.0",
			},
			"data": map[string]interface{}{
				"type": "versions",
				"id":   o.id,
				"attributes": map[string]interface{}{
					"lastModifiedTime": t.UTC().Format("2006-01-02T15:04:05.0Z"),
				},
			},
		}

		jsonPayload, err := json.Marshal(payload)
		if err != nil {
			fs.Debugf(o, "SetModTime: Error marshaling payload: %v", err)
			return fmt.Errorf("error marshaling payload: %w", err)
		}

		fs.Debugf(o, "SetModTime: Payload: %s", string(jsonPayload))

		resp, err := o.fs.doRequest(ctx, "PATCH", url, bytes.NewBuffer(jsonPayload), map[string]string{
			"Content-Type": "application/vnd.api+json",
		})
		if err != nil {
			fs.Debugf(o, "SetModTime: Error sending request: %v", err)
			return fmt.Errorf("error sending request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			fs.Debugf(o, "SetModTime: API request failed with status code: %d, body: %s", resp.StatusCode, string(body))
			return fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
		}

		// Update the object's modTime
		o.modTime = t

		fs.Debugf(o, "SetModTime: Successfully set modification time to %v", t)
		return nil
	*/
}

// Storable returns whether this object is storable
func (o *Object) Storable() bool {
	return true
}

// Update modifies the existing object in place by creating a new version
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	fs.Debugf(o, "Update: Starting for object %s", o.remote)

	// Construct the full path by joining the root and remote
	fullPath := path.Join(o.fs.root, o.remote)
	remoteDir, fileName := path.Split(fullPath)
	remoteDir = strings.TrimSuffix(remoteDir, "/")

	fs.Debugf(o, "Update: Full path: %s, dir: %s, file: %s", fullPath, remoteDir, fileName)

	// Get the folder ID for the directory containing this object
	folderID, err := o.fs.GetFolderIDByPath(ctx, remoteDir)
	if err != nil {
		fs.Debugf(o, "Update: Error getting folder ID for path '%s': %v", remoteDir, err)
		return fmt.Errorf("error getting folder ID for update: %w", err)
	}

	// Create storage location for the new version
	storageResp, err := o.fs.CreateStorageLocation(ctx, folderID, fileName)
	if err != nil {
		fs.Debugf(o, "Update: Error creating storage location: %v", err)
		return fmt.Errorf("error creating storage location: %w", err)
	}

	bucket, objectKey, err := o.fs.parseBucketAndObjectKeyFromURN(storageResp.Data.ID)
	if err != nil {
		fs.Debugf(o, "Update: Error parsing bucket and object key: %v", err)
		return fmt.Errorf("error parsing bucket and object key: %w", err)
	}

	// Upload the content to the new storage location
	err = o.fs.UploadToStorage(ctx, bucket, objectKey, in, src.Size())
	if err != nil {
		fs.Debugf(o, "Update: Error uploading content: %v", err)
		return fmt.Errorf("error uploading content: %w", err)
	}

	// Create a new version for the existing item
	newStorageUrn := fmt.Sprintf("urn:adsk.objects:os.object:%s/%s", bucket, objectKey)
	err = o.CreateNewVersion(ctx, newStorageUrn, fileName)
	if err != nil {
		fs.Debugf(o, "Update: Error creating new version: %v", err)
		return fmt.Errorf("error creating new version: %w", err)
	}

	// Update object metadata
	o.modTime = src.ModTime(ctx)
	o.size = src.Size()
	o.storageUrn = newStorageUrn

	fs.Debugf(o, "Update: Successfully updated object %s", o.remote)
	return nil
}

// FolderContentsOptions represents the options for getting folder contents
type FolderContentsOptions struct {
	FilterType                   []string
	FilterID                     []string
	FilterExtensionType          []string
	FilterLastModifiedTimeRollup string
	PageNumber                   int
	PageLimit                    int
	IncludeHidden                bool
}

// doRequest is a wrapper for HTTP requests that handles token refresh and rate limiting
func (f *Fs) doRequest(ctx context.Context, method, urlStr string, body io.Reader, headers map[string]string) (*http.Response, error) {

	// Check if token is expired or about to expire (within 30 seconds)
	if time.Until(f.tokenExpiry) < 30*time.Second {
		fs.Debugf(f, "Token is expired or about to expire, refreshing before request")
		err := f.refreshToken()
		if err != nil {
			fs.Debugf(f, "Error refreshing token: %v", err)
			return nil, fmt.Errorf("error refreshing token: %w", err)
		}
	}

	// Wait for rate limiter
	err := f.rateLimiter.Wait(ctx)
	if err != nil {
		fs.Debugf(f, "Rate limiter error: %v", err)
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
	if err != nil {
		fs.Debugf(f, "Error creating request: %v", err)
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Set default headers
	req.Header.Set("Authorization", fmt.Sprintf("%s %s", f.tokenInfo.TokenType, f.tokenInfo.AccessToken))
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{}

	// Try the request
	resp, err := client.Do(req)
	if err != nil {
		fs.Debugf(f, "Error sending request: %v", err)
		return nil, fmt.Errorf("error sending request: %w", err)
	}

	return resp, nil
}

// refreshToken refreshes the access token using the refresh token
func (f *Fs) refreshToken() error {

	if f.tokenInfo.RefreshToken == "" {
		return fmt.Errorf("refresh token is empty")
	}

	// Encode client_id:client_secret to base64
	authHeader := base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", f.opt.ClientID, f.opt.ClientSecret)))

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", f.tokenInfo.RefreshToken)

	// Debug log for request body
	encodedData := data.Encode()

	req, err := http.NewRequest("POST", "https://developer.api.autodesk.com/authentication/v2/token", strings.NewReader(encodedData))
	if err != nil {
		return fmt.Errorf("failed to create refresh token request: %w", err)
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Authorization", "Basic "+authHeader)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send refresh token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Try to parse the error response for more details
		var errorResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}

		if err := json.Unmarshal(body, &errorResp); err == nil && errorResp.Error != "" {
			return fmt.Errorf("refresh token request failed: %s - %s", errorResp.Error, errorResp.ErrorDescription)
		}

		return fmt.Errorf("refresh token request failed with status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var tokenResp api.TokenResponse
	err = json.Unmarshal(body, &tokenResp)
	if err != nil {
		return fmt.Errorf("failed to decode refresh token response: %w", err)
	}

	// Validate the response contains the required fields
	if tokenResp.AccessToken == "" {
		return fmt.Errorf("refresh token response missing access_token")
	}

	f.tokenInfo.AccessToken = tokenResp.AccessToken
	f.tokenInfo.TokenType = tokenResp.TokenType
	// Calculate expiry time from expires_in
	f.tokenExpiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	f.tokenInfo.Expiry = f.tokenExpiry

	// Only update refresh token if a new one was provided
	if tokenResp.RefreshToken != "" {
		f.tokenInfo.RefreshToken = tokenResp.RefreshToken

		// Save the new token information
		err = f.SaveTokenInfo()
		if err != nil {
			fs.Errorf(f, "Failed to save new token information: %v", err)
			// Note: We're not returning an error here, as the token refresh was still successful
		}
	}

	fs.Debugf(f, "Access token refreshed successfully. New expiry: %v", f.tokenExpiry)

	return nil
}

// SaveTokenInfo saves the updated token information to the configuration
func (f *Fs) SaveTokenInfo() error {
	fs.Debugf(f, "Saving updated token information")

	// Get the config name
	configName := f.name
	if configName == "" {
		configName = "acc"
	}

	// Marshal the token info back to JSON
	tokenJSON, err := json.Marshal(f.tokenInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal token info: %w", err)
	}

	// Set the token in the config
	err = fs.ConfigFileSet(configName, "token", string(tokenJSON))
	if err != nil {
		return fmt.Errorf("failed to set token: %w", err)
	}

	// Save the configuration
	config.SaveConfig()

	fs.Debugf(f, "Token information saved successfully")
	return nil
}

// CreateFolder creates a new folder in ACC
func (f *Fs) CreateFolder(ctx context.Context, parentFolderID, folderName string) (*api.Folder, error) {
	// Prepare the request payload
	payload := map[string]interface{}{
		"jsonapi": map[string]string{
			"version": "1.0",
		},
		"data": map[string]interface{}{
			"type": "folders",
			"attributes": map[string]interface{}{
				"name": folderName,
				"extension": map[string]string{
					"type":    "folders:autodesk.bim360:Folder",
					"version": "1.0",
				},
			},
			"relationships": map[string]interface{}{
				"parent": map[string]interface{}{
					"data": map[string]string{
						"type": "folders",
						"id":   parentFolderID,
					},
				},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("error marshaling payload: %w", err)
	}

	// Prepare the request URL
	url := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/folders", f.opt.ProjectID)

	// Send the request
	resp, err := f.doRequest(ctx, "POST", url, bytes.NewBuffer(jsonPayload), map[string]string{
		"Content-Type": "application/vnd.api+json",
	})
	if err != nil {
		return nil, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	// Check the response
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status code: %d, body: %s", resp.StatusCode, string(body))
	}

	// Parse the response to get the new folder
	var result struct {
		Data api.Folder `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	fs.Debugf(f, "Successfully created folder: %s with ID: %s", folderName, result.Data.ID)
	return &result.Data, nil
}

// GetFolderDetails retrieves the details of a folder by its ID
func (f *Fs) GetFolderDetails(ctx context.Context, folderID string) (*api.Folder, error) {
	url := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/folders/%s", f.opt.ProjectID, folderID)

	fs.Debugf(f, "Requesting folder details from: %s", url)

	resp, err := f.doRequest(ctx, "GET", url, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	//fs.Debugf(f, "Response status: %s", resp.Status)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fs.Debugf(f, "API request failed with status code: %d, body: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
	}

	var result struct {
		Data api.Folder `json:"data"`
	}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	fs.Debugf(f, "Retrieved folder details for folder ID: %s", folderID)

	return &result.Data, nil
}

// CreateSignedS3DownloadLink creates a signed S3 download link for the specified object
func (f *Fs) CreateSignedS3DownloadLink(ctx context.Context, bucket, objectKey string) (*api.SignedS3DownloadResponse, error) {
	// URL encode the bucket and objectKey as they may contain special characters
	encodedBucket := url.PathEscape(bucket)
	encodedObjectKey := url.PathEscape(objectKey)

	urlStr := fmt.Sprintf("https://developer.api.autodesk.com/oss/v2/buckets/%s/objects/%s/signeds3download", encodedBucket, encodedObjectKey)

	fs.Debugf(f, "Requesting signed S3 download link from: %s", urlStr)

	resp, err := f.doRequest(ctx, "GET", urlStr, nil, map[string]string{
		"Content-Type": "application/json",
	})
	if err != nil {
		return nil, fmt.Errorf("error making request for signed S3 download link: %w", err)
	}
	defer resp.Body.Close()

	//fs.Debugf(f, "Response status: %s", resp.Status)

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
	}

	var signedURLResp api.SignedS3DownloadResponse
	err = json.NewDecoder(resp.Body).Decode(&signedURLResp)
	if err != nil {
		return nil, fmt.Errorf("error decoding signed S3 download link response: %w", err)
	}

	fs.Debugf(f, "Retrieved signed S3 download link for bucket: %s, object: %s", bucket, objectKey)

	return &signedURLResp, nil
}

func (f *Fs) CreateSignedS3UploadLinks(ctx context.Context, bucket, objectKey string, numParts int) (*api.SignedS3UploadResponse, error) {
	encodedBucket := url.PathEscape(bucket)
	encodedObjectKey := url.PathEscape(objectKey)
	urlStr := fmt.Sprintf("https://developer.api.autodesk.com/oss/v2/buckets/%s/objects/%s/signeds3upload?parts=%d",
		encodedBucket, encodedObjectKey, numParts)

	resp, err := f.doRequest(ctx, "GET", urlStr, nil, map[string]string{
		"Content-Type": "application/json",
	})
	if err != nil {
		return nil, fmt.Errorf("error requesting signed S3 upload links: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get signed upload links: status code %d, body: %s", resp.StatusCode, string(body))
	}

	var uploadResp api.SignedS3UploadResponse
	err = json.NewDecoder(resp.Body).Decode(&uploadResp)
	if err != nil {
		return nil, fmt.Errorf("error decoding signed S3 upload response: %w", err)
	}

	return &uploadResp, nil
}

// GetFolderContents retrieves the contents of a folder with pagination and filtering
func (f *Fs) GetFolderContents(ctx context.Context, folderID string, opts FolderContentsOptions) (*api.FolderContentsResponse, error) {
	encodedFolderID := url.PathEscape(folderID)
	baseURL := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/folders/%s/contents", f.opt.ProjectID, encodedFolderID)

	// Build query parameters
	query := url.Values{}
	if len(opts.FilterType) > 0 {
		query["filter[type]"] = opts.FilterType
	}
	if len(opts.FilterID) > 0 {
		query["filter[id]"] = opts.FilterID
	}
	if len(opts.FilterExtensionType) > 0 {
		query["filter[extension.type]"] = opts.FilterExtensionType
	}
	if opts.FilterLastModifiedTimeRollup != "" {
		query.Set("filter[lastModifiedTimeRollup]", opts.FilterLastModifiedTimeRollup)
	}
	if opts.PageNumber >= 0 {
		query.Set("page[number]", strconv.Itoa(opts.PageNumber))
	}
	if opts.PageLimit > 0 {
		query.Set("page[limit]", strconv.Itoa(opts.PageLimit))
	}
	if opts.IncludeHidden {
		query.Set("includeHidden", "true")
	}

	// Append query parameters to the base URL
	fullURL := baseURL
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	fs.Debugf(f, "Requesting folder contents from: %s", fullURL)

	resp, err := f.doRequest(ctx, "GET", fullURL, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	//fs.Debugf(f, "Response status: %s", resp.Status)

	if resp.StatusCode != http.StatusOK {
		fs.Debugf(f, "API request failed with status code: %d", resp.StatusCode)
		return nil, fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
	}

	var result api.FolderContentsResponse
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		fs.Debugf(f, "Error decoding response: %v", err)
		return nil, err
	}

	fs.Debugf(f, "Retrieved %d folder contents", len(result.Data))

	return &result, nil
}

// ProcessFolderContents processes the folder contents response
func (f *Fs) ProcessFolderContents(ctx context.Context, contents *api.FolderContentsResponse) ([]api.Folder, []api.Version, error) {
	fs.Debugf(f, "Starting ProcessFolderContents")

	var folders []api.Folder
	var versions []api.Version

	for _, content := range contents.Data {
		contentType := content.(map[string]interface{})["type"].(string)
		contentId := content.(map[string]interface{})["id"].(string)
		fs.Debugf(f, "Processing contentType contentId: %s %s", contentType, contentId)
		switch contentType {
		case "folders":
			folder, err := f.GetFolderDetails(ctx, contentId)
			if err != nil {
				fs.Debugf(f, "Error getting folder details for folder %s: %v", contentId, err)
				return nil, nil, fmt.Errorf("error getting folder details for folder %s: %w", contentId, err)
			}
			folders = append(folders, *folder)
		case "items":
			version, err := f.GetItemByID(ctx, contentId)
			if err != nil {
				fs.Debugf(f, "Error getting version for item %s: %v", contentId, err)
				return nil, nil, fmt.Errorf("error getting version for item %s: %w", contentId, err)
			}
			versions = append(versions, *version)

		default:
			fs.Debugf(f, "Unknown content type: %T", contentType)
		}
	}

	fs.Debugf(f, "Processed %d folders and %d versions", len(folders), len(versions))

	return folders, versions, nil
}

// GetTopFolders retrieves the top folders for a given project
func (f *Fs) GetTopFolders(ctx context.Context) ([]api.Folder, error) {
	url := fmt.Sprintf("https://developer.api.autodesk.com/project/v1/hubs/%s/projects/%s/topFolders", f.opt.HubID, f.opt.ProjectID)

	fs.Debugf(f, "Requesting top folders from: %s", url)

	resp, err := f.doRequest(ctx, "GET", url, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	//fs.Debugf(f, "Response status: %s", resp.Status)

	// Debug log for response headers
	//fs.Debugf(f, "GetTopFolders response headers: %v", resp.Header)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fs.Debugf(f, "Error reading response body: %v", err)
		return nil, err
	}

	// Debug log for response body
	//fs.Debugf(f, "GetTopFolders response body: %s", string(body))

	if resp.StatusCode != http.StatusOK {
		fs.Debugf(f, "API request failed with status code: %d, body: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
	}

	var result api.FoldersResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		fs.Debugf(f, "Error decoding response: %v", err)
		return nil, err
	}

	fs.Debugf(f, "Retrieved %d top folders", len(result.Data))

	return result.Data, nil
}

// ComparisonType represents the type of comparison for filtering
type ComparisonType string

const (
	LessThan           ComparisonType = "lt"
	LessThanOrEqual    ComparisonType = "le"
	EqualTo            ComparisonType = "eq"
	GreaterThanOrEqual ComparisonType = "ge"
	GreaterThan        ComparisonType = "gt"
	StringStartsWith   ComparisonType = "starts"
	StringEndsWith     ComparisonType = "ends"
	StringContains     ComparisonType = "contains"
)

// FilterOption represents a single filter option
type FilterOption struct {
	FieldName string
	CompType  ComparisonType
	Value     string
}

// SearchOptions represents the options for searching folders
type SearchOptions struct {
	Filters    []FilterOption
	PageNumber int
}

// SearchFolderContents searches for contents within a folder
func (f *Fs) SearchFolderContents(ctx context.Context, folderURN string, opts SearchOptions) (*api.FolderContentsResponse, error) {
	fs.Debugf(f, "SearchFolderContents: Starting for folderURN: %s", folderURN)

	encodedFolderURN := url.PathEscape(folderURN)
	baseURL := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/folders/%s/search", f.opt.ProjectID, encodedFolderURN)

	// Build query parameters
	query := url.Values{}

	// Add filter parameters
	for _, filter := range opts.Filters {
		filterKey := fmt.Sprintf("filter[%s]-%s", filter.FieldName, filter.CompType)
		query.Add(filterKey, filter.Value)
	}

	// Add page number
	if opts.PageNumber >= 0 && opts.PageNumber <= 49 {
		query.Set("page[number]", strconv.Itoa(opts.PageNumber))
	} else {
		fs.Debugf(f, "SearchFolderContents: Invalid page number: %d. Using default.", opts.PageNumber)
	}

	// Append query parameters to the base URL
	fullURL := baseURL
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	fs.Debugf(f, "SearchFolderContents: Searching folder contents from: %s", fullURL)

	resp, err := f.doRequest(ctx, "GET", fullURL, nil, nil)
	if err != nil {
		fs.Debugf(f, "SearchFolderContents: Error making GET request: %v", err)
		return nil, fmt.Errorf("error searching folder contents: %w", err)
	}
	defer resp.Body.Close()

	fs.Debugf(f, "SearchFolderContents: Received response with status code: %d", resp.StatusCode)

	// Read the entire response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fs.Debugf(f, "SearchFolderContents: Error reading response body: %v", err)
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	// Log the response body
	fs.Debugf(f, "SearchFolderContents: Response body: %s", string(body))

	if resp.StatusCode != http.StatusOK {
		fs.Debugf(f, "SearchFolderContents: API request failed. Status code: %d, Response body: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
	}

	var result api.FolderContentsResponse
	err = json.Unmarshal(body, &result)
	if err != nil {
		fs.Debugf(f, "SearchFolderContents: Error decoding response: %v", err)
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	fs.Debugf(f, "SearchFolderContents: Retrieved %d search results", len(result.Data))

	return &result, nil
}

// GetFolderIDByPath finds the folder ID based on the given path
func (f *Fs) GetFolderIDByPath(ctx context.Context, folderPath string) (string, error) {
	fs.Debugf(f, "Starting GetFolderIDByPath for path: %s", folderPath)

	// Ensure we have the folder structure
	if f.folderStructure == nil {
		var err error
		f.folderStructure, err = f.GetFullFolderStructure(ctx)
		if err != nil {
			return "", fmt.Errorf("error getting full folder structure: %w", err)
		}
	}

	// Normalize the input path
	normalizedPath := path.Clean("/" + folderPath)

	// If the path is relative to root, prepend the root path
	if !strings.HasPrefix(normalizedPath, f.root) && normalizedPath != "/" {
		normalizedPath = path.Join(f.root, normalizedPath)
	}

	fs.Debugf(f, "GetFolderIDByPath: Normalized path: %s", normalizedPath)

	// Check if the path exists in our structure
	for idPath, namePath := range f.folderStructure {
		if namePath == normalizedPath {
			// Extract the folder ID from the idPath
			parts := strings.Split(idPath, "/")
			return parts[len(parts)-1], nil
		}
	}

	// If we're here, we couldn't find the folder
	if folderPath == "" && len(f.folderStructure) == 1 {
		// Special case: empty path and only one top folder
		for idPath := range f.folderStructure {
			parts := strings.Split(idPath, "/")
			return parts[len(parts)-1], nil
		}
	}

	return "", fmt.Errorf("folder not found: %s", folderPath)
}

// GetAllFolderContents retrieves all contents of a folder, handling pagination
func (f *Fs) GetAllFolderContents(ctx context.Context, folderID string, opts FolderContentsOptions) ([]api.Folder, []api.Version, error) {
	fs.Debugf(f, "Starting GetAllFolderContents for folder ID: %s", folderID)

	var allFolders []api.Folder
	var allVersions []api.Version
	pageNumber := 0
	const pageLimit = 200 // Adjust this value as needed

	for {
		opts.PageNumber = pageNumber
		opts.PageLimit = pageLimit

		fs.Debugf(f, "Requesting folder contents for page %d with limit %d", pageNumber, pageLimit)
		contents, err := f.GetFolderContents(ctx, folderID, opts)
		if err != nil {
			fs.Debugf(f, "Error getting folder contents for page %d: %v", pageNumber, err)
			return nil, nil, fmt.Errorf("error getting folder contents for page %d: %w", pageNumber, err)
		}

		fs.Debugf(f, "Processing folder contents for page %d", pageNumber)
		folders, versions, err := f.ProcessFolderContents(ctx, contents)
		if err != nil {
			fs.Debugf(f, "Error processing folder contents for page %d: %v", pageNumber, err)
			return nil, nil, fmt.Errorf("error processing folder contents for page %d: %w", pageNumber, err)
		}

		allFolders = append(allFolders, folders...)
		allVersions = append(allVersions, versions...)

		fs.Debugf(f, "Retrieved %d folders and %d versions from page %d", len(folders), len(versions), pageNumber)

		// Check if we've reached the last page
		if len(contents.Data) < pageLimit {
			fs.Debugf(f, "Reached last page of folder contents at page %d", pageNumber)
			break
		}

		pageNumber++
	}

	fs.Debugf(f, "Retrieved a total of %d folders and %d versions", len(allFolders), len(allVersions))

	return allFolders, allVersions, nil
}

// GetItemIDByPath finds the item ID based on the given path
func (f *Fs) GetItemIDByPath(ctx context.Context, itemPath string) (string, error) {
	fs.Debugf(f, "GetItemIDByPath: Starting for path: %s", itemPath)

	// Split the path into folder path and item name
	dir, itemName := path.Split(itemPath)
	dir = strings.TrimSuffix(dir, "/")

	fs.Debugf(f, "GetItemIDByPath: Folder path: %s, Item name: %s", dir, itemName)

	// Get the folder ID for the parent directory
	folderID, err := f.GetFolderIDByPath(ctx, dir)
	if err != nil {
		fs.Debugf(f, "GetItemIDByPath: Error getting folder ID for path '%s': %v", dir, err)
		return "", fmt.Errorf("error getting folder ID for path '%s': %w", dir, err)
	}

	fs.Debugf(f, "GetItemIDByPath: Found folder ID: %s for path: %s", folderID, dir)

	// Search for the item in the folder
	opts := SearchOptions{
		Filters: []FilterOption{
			{FieldName: "attributes.displayName", CompType: StringContains, Value: itemName},
		},
		PageNumber: 0,
	}

	fs.Debugf(f, "GetItemIDByPath: Searching for item '%s' in folder ID: %s", itemName, folderID)

	searchResults, err := f.SearchFolderContents(ctx, folderID, opts)
	if err != nil {
		fs.Debugf(f, "GetItemIDByPath: Error searching for item '%s': %v", itemName, err)
		return "", fmt.Errorf("error searching for item '%s': %w", itemName, err)
	}

	fs.Debugf(f, "GetItemIDByPath: Search returned %d results", len(searchResults.Data))

	for _, result := range searchResults.Data {
		item, ok := result.(map[string]interface{})
		if !ok {
			fs.Debugf(f, "GetItemIDByPath: Skipping non-map item in search results")
			continue
		}

		fs.Debugf(f, "GetItemIDByPath: Processing search result item: %v", item)

		// Check if the item name matches
		if name, ok := item["attributes"].(map[string]interface{})["displayName"].(string); ok && name == itemName {
			// Check if the parent folder ID matches
			if parent, ok := item["relationships"].(map[string]interface{})["parent"].(map[string]interface{}); ok {
				if data, ok := parent["data"].(map[string]interface{}); ok {
					if parentID, ok := data["id"].(string); ok && parentID == folderID {
						if id, ok := item["id"].(string); ok {
							fs.Debugf(f, "GetItemIDByPath: Found matching item. Name: %s, ID: %s, Parent Folder ID: %s", name, id, parentID)
							return id, nil
						}
					}
				}
			}
		}
	}

	fs.Debugf(f, "GetItemIDByPath: Item not found: %s in folder: %s", itemName, folderID)
	return "", fmt.Errorf("item not found: %s in folder: %s", itemName, folderID)
}

// GetItemByID retrieves the latest version of an item by its ID
func (f *Fs) GetItemByID(ctx context.Context, itemID string) (*api.Version, error) {
	url := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/items/%s/tip", f.opt.ProjectID, itemID)

	fs.Debugf(f, "Requesting item tip version from: %s", url)

	resp, err := f.doRequest(ctx, "GET", url, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	//fs.Debugf(f, "Response status: %s", resp.Status)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fs.Debugf(f, "API request failed with status code: %d, body: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
	}

	var result api.Version
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	fs.Debugf(f, "Retrieved tip version for item ID: %s", itemID)

	return &result, nil
}

// GetFolderPath retrieves the full path of a folder by its ID using the existing path structure
func (f *Fs) GetFolderPath(ctx context.Context, folderId string, folderPaths map[string]string) (string, string, error) {
	fs.Debugf(f, "Getting folder path for folder ID: %s", folderId)

	// Check if the folder ID exists in the folderPaths map
	for idPath, namePath := range folderPaths {
		parts := strings.Split(idPath, "/")
		if parts[len(parts)-1] == folderId {
			fs.Debugf(f, "Found folder path for folder ID: %s", folderId)
			return idPath, namePath, nil
		}
	}

	// If the folder ID is not found in the map, return an error
	return "", "", fmt.Errorf("folder ID not found in path structure: %s", folderId)
}

// GetFullFolderStructure retrieves the full folder structure
func (f *Fs) GetFullFolderStructure(ctx context.Context) (map[string]string, error) {
	// If we already have the folder structure, return it
	if f.folderStructure != nil {
		return f.folderStructure, nil
	}

	fs.Debugf(f, "Getting full folder structure")

	// Get top folders
	topFolders, err := f.GetTopFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting top folders: %w", err)
	}

	folderPaths := make(map[string]string)

	for _, topFolder := range topFolders {
		// Save the top folder name and ID in folderPaths
		topFolderIdPath := "/" + topFolder.ID
		topFolderNamePath := "/" + topFolder.Attributes.Name
		folderPaths[topFolderIdPath] = topFolderNamePath

		fs.Debugf(f, "Processing top folder: %s (ID: %s)", topFolder.Attributes.Name, topFolder.ID)

		err := f.recurseFolderStructure(ctx, topFolder.ID, topFolderIdPath, topFolderNamePath, folderPaths)
		if err != nil {
			fs.Errorf(f, "Error processing folder structure for top folder %s: %v", topFolder.ID, err)
		}
	}

	fs.Debugf(f, "Retrieved full folder structure with %d folders", len(folderPaths))

	// Debug: Print all folder paths
	fs.Debugf(f, "Full folder structure:")
	for idPath, namePath := range folderPaths {
		fs.Debugf(f, "ID Path: %s, Name Path: %s", idPath, namePath)
	}

	f.folderStructure = folderPaths
	return folderPaths, nil
}

// recurseFolderStructure recursively traverses the folder structure
func (f *Fs) recurseFolderStructure(ctx context.Context, folderId string, parentIdPath string, parentNamePath string, folderPaths map[string]string) error {
	fs.Debugf(f, "Starting recurseFolderStructure for folder ID: %s", folderId)

	// Get folder contents
	opts := FolderContentsOptions{
		FilterType:    []string{"folders"},
		IncludeHidden: false,
	}

	contents, err := f.GetFolderContents(ctx, folderId, opts)
	if err != nil {
		fs.Debugf(f, "Error getting folder contents for folder %s: %v", folderId, err)
		return fmt.Errorf("error getting folder contents for folder %s: %w", folderId, err)
	}

	fs.Debugf(f, "Retrieved %d items in folder ID: %s", len(contents.Data), folderId)

	// Process subfolders
	for _, item := range contents.Data {
		subFolder, ok := item.(map[string]interface{})
		if !ok {
			fs.Debugf(f, "Skipping non-folder item in folder ID: %s", folderId)
			continue
		}

		subFolderId, ok := subFolder["id"].(string)
		if !ok {
			fs.Debugf(f, "Skipping item with invalid ID in folder ID: %s", folderId)
			continue
		}

		subFolderName, ok := subFolder["attributes"].(map[string]interface{})["name"].(string)
		if !ok {
			fs.Debugf(f, "Skipping item with invalid name in folder ID: %s", folderId)
			continue
		}

		fs.Debugf(f, "Processing subfolder: %s (ID: %s)", subFolderName, subFolderId)

		// Construct the paths for the subfolder
		idPath := path.Join(parentIdPath, subFolderId)
		namePath := path.Join(parentNamePath, subFolderName)

		fs.Debugf(f, "Storing subfolder path: ID Path: %s, Name Path: %s", idPath, namePath)

		folderPaths[idPath] = namePath

		// Recursively process the subfolder
		err = f.recurseFolderStructure(ctx, subFolderId, idPath, namePath, folderPaths)
		if err != nil {
			fs.Errorf(f, "Error processing subfolder %s: %v", subFolderId, err)
		}
	}

	fs.Debugf(f, "Completed recurseFolderStructure for folder ID: %s", folderId)
	return nil
}

// GetObjectDetails retrieves the details of an object in Autodesk Construction Cloud
func (f *Fs) GetObjectDetails(ctx context.Context, bucketKey, objectKey string) (*api.ObjectDetails, error) {
	// URL encode the objectKey as it may contain special characters
	encodedObjectKey := url.PathEscape(objectKey)

	url := fmt.Sprintf("https://developer.api.autodesk.com/oss/v2/buckets/%s/objects/%s/details", bucketKey, encodedObjectKey)

	fs.Debugf(f, "Requesting object details from: %s", url)

	resp, err := f.doRequest(ctx, "GET", url, nil, map[string]string{
		"Content-Type": "application/json",
	})
	if err != nil {
		return nil, fmt.Errorf("error making request: %w", err)
	}
	defer resp.Body.Close()

	//fs.Debugf(f, "Response status: %s", resp.Status)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
	}

	var details api.ObjectDetails
	err = json.NewDecoder(resp.Body).Decode(&details)
	if err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	fs.Debugf(f, "Retrieved object details for bucket: %s, object: %s", bucketKey, objectKey)

	return &details, nil
}

// CreateStorageLocation creates a new storage location in the specified project and folder
func (f *Fs) CreateStorageLocation(ctx context.Context, folderID, fileName string) (*api.StorageLocationResponse, error) {
	url := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/storage", f.opt.ProjectID)

	payload := map[string]interface{}{
		"jsonapi": map[string]string{
			"version": "1.0",
		},
		"data": map[string]interface{}{
			"type": "objects",
			"attributes": map[string]string{
				"name": fileName,
			},
			"relationships": map[string]interface{}{
				"target": map[string]interface{}{
					"data": map[string]string{
						"type": "folders",
						"id":   folderID,
					},
				},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("error marshaling payload: %w", err)
	}

	resp, err := f.doRequest(ctx, "POST", url, bytes.NewBuffer(jsonPayload), map[string]string{
		"Content-Type": "application/vnd.api+json",
	})
	if err != nil {
		return nil, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var storageLocationResp api.StorageLocationResponse
	err = json.NewDecoder(resp.Body).Decode(&storageLocationResp)
	if err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	fs.Debugf(f, "Created storage location for file: %s in folder: %s", fileName, folderID)

	return &storageLocationResp, nil
}

func (f *Fs) parseBucketAndObjectKeyFromURN(urn string) (string, string, error) {
	fs.Debugf(f, "parseBucketAndObjectKeyFromURN: Parsing URN: %s", urn)
	// urn:adsk.objects:os.object:wip.dm.prod/26c58ff9-04c4-457e-82ee-3846395bcfba.pdf
	parts := strings.Split(urn, ":")
	if len(parts) != 4 || parts[0] != "urn" || parts[1] != "adsk.objects" || parts[2] != "os.object" {
		return "", "", fmt.Errorf("invalid URN format: %s", urn)
	}

	bucketAndObject := parts[3]
	bucketAndObjectParts := strings.SplitN(bucketAndObject, "/", 2)
	if len(bucketAndObjectParts) != 2 {
		return "", "", fmt.Errorf("invalid bucket/object format in URN: %s", urn)
	}

	bucket := bucketAndObjectParts[0]
	objectKey := bucketAndObjectParts[1]

	fs.Debugf(f, "parseBucketAndObjectKeyFromURN: Parsed bucket: %s, object key: %s", bucket, objectKey)
	return bucket, objectKey, nil
}

func (f *Fs) FinalizeUpload(ctx context.Context, bucket, objectKey, uploadKey string) error {
	fs.Debugf(f, "FinalizeUpload: Starting for bucket: %s, objectKey: %s, uploadKey: %s", bucket, objectKey, uploadKey)

	encodedBucket := url.PathEscape(bucket)
	encodedObjectKey := url.PathEscape(objectKey)
	urlStr := fmt.Sprintf("https://developer.api.autodesk.com/oss/v2/buckets/%s/objects/%s/signeds3upload",
		encodedBucket, encodedObjectKey)

	fs.Debugf(f, "FinalizeUpload: Constructed URL: %s", urlStr)

	payload := map[string]string{
		"uploadKey": uploadKey,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		fs.Debugf(f, "FinalizeUpload: Error marshaling payload: %v", err)
		return fmt.Errorf("error marshaling payload: %w", err)
	}

	fs.Debugf(f, "FinalizeUpload: Sending POST request to finalize upload")

	resp, err := f.doRequest(ctx, "POST", urlStr, bytes.NewBuffer(jsonPayload), map[string]string{
		"Content-Type":            "application/json",
		"x-ads-meta-Content-Type": "application/octet-stream",
	})
	if err != nil {
		fs.Debugf(f, "FinalizeUpload: Error making POST request: %v", err)
		return fmt.Errorf("error finalizing upload: %w", err)
	}
	defer resp.Body.Close()

	fs.Debugf(f, "FinalizeUpload: Received response with status code: %d", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fs.Debugf(f, "FinalizeUpload: Failed to finalize upload. Status code: %d, Response body: %s", resp.StatusCode, string(body))
		return fmt.Errorf("failed to finalize upload: status code %d, body: %s", resp.StatusCode, string(body))
	}

	fs.Debugf(f, "FinalizeUpload: Successfully finalized upload for bucket: %s, objectKey: %s", bucket, objectKey)
	return nil
}

func (f *Fs) UploadChunk(ctx context.Context, signedURL string, data []byte, partNumber int) error {
	req, err := http.NewRequestWithContext(ctx, "PUT", signedURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("error creating request for part %d: %w", partNumber, err)
	}

	req.Header.Set("Content-Length", strconv.Itoa(len(data)))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending request for part %d: %w", partNumber, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed for part %d: status code %d, body: %s", partNumber, resp.StatusCode, string(body))
	}

	fs.Debugf(f, "Successfully uploaded part %d", partNumber)
	return nil
}

func (f *Fs) UploadFile(ctx context.Context, folderID, fileName string, reader io.Reader, size int64) (*Object, error) {
	// Create storage location
	storageResp, err := f.CreateStorageLocation(ctx, folderID, fileName)
	if err != nil {
		return nil, fmt.Errorf("error creating storage location: %w", err)
	}

	bucket, objectKey, err := f.parseBucketAndObjectKeyFromURN(storageResp.Data.ID)
	if err != nil {
		return nil, fmt.Errorf("error parsing bucket and object key: %w", err)
	}

	// Compute number of chunks (max 25)
	const maxChunks = 25
	const minChunkSize = 5 * 1024 * 1024 // 5 MB minimum chunk size

	chunkSize := size / maxChunks
	if chunkSize < minChunkSize {
		chunkSize = minChunkSize
	}
	numChunks := int(math.Ceil(float64(size) / float64(chunkSize)))
	if numChunks > maxChunks {
		numChunks = maxChunks
		chunkSize = size / int64(numChunks)
	}

	// Get signed upload URLs
	uploadResp, err := f.CreateSignedS3UploadLinks(ctx, bucket, objectKey, numChunks)
	if err != nil {
		return nil, fmt.Errorf("error getting signed upload URLs: %w", err)
	}

	// Upload chunks with retry logic
	const maxRetries = 1
	const retryDelay = 2 * time.Second

	for i := 0; i < numChunks; i++ {
		chunk := make([]byte, chunkSize)
		n, err := io.ReadFull(reader, chunk)
		if err != nil && err != io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("error reading chunk %d: %w", i+1, err)
		}
		chunk = chunk[:n] // Trim chunk if it's the last one and smaller

		var uploadErr error
		for retry := 0; retry < maxRetries; retry++ {
			uploadErr = f.UploadChunk(ctx, uploadResp.URLs[i], chunk, i+1)
			if uploadErr == nil {
				break // Successful upload, exit retry loop
			}

			fs.Debugf(f, "Error uploading chunk %d (attempt %d): %v", i+1, retry+1, uploadErr)

			if retry < maxRetries-1 {
				// Wait before retrying
				select {
				case <-time.After(retryDelay):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}

		if uploadErr != nil {
			return nil, fmt.Errorf("failed to upload chunk %d after %d attempts: %w", i+1, maxRetries, uploadErr)
		}

		fs.Debugf(f, "Successfully uploaded chunk %d", i+1)
	}

	// Finalize upload
	err = f.FinalizeUpload(ctx, bucket, objectKey, uploadResp.UploadKey)
	if err != nil {
		return nil, fmt.Errorf("error finalizing upload: %w", err)
	}

	// Create item for the uploaded file
	storageUrn := fmt.Sprintf("urn:adsk.objects:os.object:%s/%s", bucket, objectKey)
	item, err := f.CreateItemFromUpload(ctx, folderID, fileName, storageUrn)
	if err != nil {
		return nil, fmt.Errorf("error creating item for uploaded file: %w", err)
	}

	// Get object details
	objectDetails, err := f.GetObjectDetails(ctx, bucket, objectKey)
	if err != nil {
		return nil, fmt.Errorf("error getting object details: %w", err)
	}

	// Create and return the Object
	obj := &Object{
		fs:            f,
		remote:        path.Join(path.Dir(item.Data.Attributes.DisplayName), fileName),
		size:          objectDetails.Size,
		modTime:       item.Data.Attributes.LastModifiedTime,
		id:            item.Data.Relationships.Tip.Data.ID,
		itemID:        item.Data.ID,
		storageUrn:    storageUrn,
		objectDetails: objectDetails,
	}
	return obj, nil
}

// CreateItemFromUpload creates an item for the uploaded file and updates the folder structure
func (f *Fs) CreateItemFromUpload(ctx context.Context, folderID, fileName, storageUrn string) (*api.Item, error) {
	fs.Debugf(f, "CreateItemFromUpload: Starting for file: %s in folder: %s", fileName, folderID)

	url := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/items", f.opt.ProjectID)

	payload := map[string]interface{}{
		"jsonapi": map[string]string{
			"version": "1.0",
		},
		"data": map[string]interface{}{
			"type": "items",
			"attributes": map[string]interface{}{
				"displayName": fileName,
				"extension": map[string]interface{}{
					"type":    "items:autodesk.bim360:File",
					"version": "1.0",
				},
			},
			"relationships": map[string]interface{}{
				"tip": map[string]interface{}{
					"data": map[string]string{
						"type": "versions",
						"id":   "1",
					},
				},
				"parent": map[string]interface{}{
					"data": map[string]string{
						"type": "folders",
						"id":   folderID,
					},
				},
			},
		},
		"included": []map[string]interface{}{
			{
				"type": "versions",
				"id":   "1",
				"attributes": map[string]interface{}{
					"name": fileName,
					"extension": map[string]interface{}{
						"type":    "versions:autodesk.bim360:File",
						"version": "1.0",
					},
				},
				"relationships": map[string]interface{}{
					"storage": map[string]interface{}{
						"data": map[string]string{
							"type": "objects",
							"id":   storageUrn,
						},
					},
				},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		fs.Debugf(f, "CreateItemFromUpload: Error marshaling payload: %v", err)
		return nil, fmt.Errorf("error marshaling payload: %w", err)
	}

	fs.Debugf(f, "CreateItemFromUpload: Sending POST request to %s", url)

	resp, err := f.doRequest(ctx, "POST", url, bytes.NewBuffer(jsonPayload), map[string]string{
		"Content-Type": "application/vnd.api+json",
		"Accept":       "application/vnd.api+json",
	})
	if err != nil {
		fs.Debugf(f, "CreateItemFromUpload: Error sending request: %v", err)
		return nil, fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		fs.Debugf(f, "CreateItemFromUpload: API request failed with status code: %d, body: %s", resp.StatusCode, string(body))
		return nil, fmt.Errorf("API request failed with status code: %d", resp.StatusCode)
	}

	// Parse the API response to get the item details
	var item api.Item
	if err := json.NewDecoder(resp.Body).Decode(&item); err != nil {
		fs.Debugf(f, "CreateItemFromUpload: Error decoding item response: %v", err)
		return nil, fmt.Errorf("error decoding item response: %w", err)
	}

	// Update folder structure with all the information
	err = f.updateFolderStructureWithNewFile(ctx, folderID, item.Data.Attributes.DisplayName, &item)
	if err != nil {
		fs.Debugf(f, "CreateItemFromUpload: Error updating folder structure: %v", err)
	}

	fs.Debugf(f, "CreateItemFromUpload: Successfully created item for file: %s in folder: %s", fileName, folderID)
	return &item, nil
}

// updateFolderStructureWithNewFile updates the folderStructure with a newly created file
func (f *Fs) updateFolderStructureWithNewFile(ctx context.Context, folderID, fileName string, item *api.Item) error {
	// Ensure we have the folder structure
	if f.folderStructure == nil {
		var err error
		f.folderStructure, err = f.GetFullFolderStructure(ctx)
		if err != nil {
			return fmt.Errorf("error getting full folder structure: %w", err)
		}
	}

	// Find the folder path for the given folderID
	var folderPath string
	for idPath := range f.folderStructure {
		parts := strings.Split(idPath, "/")
		if parts[len(parts)-1] == folderID {
			folderPath = idPath
			break
		}
	}

	if folderPath == "" {
		return fmt.Errorf("folder not found in structure: %s", folderID)
	}

	// Add the new file to the folder structure
	newFilePath := path.Join(folderPath, fileName)
	f.folderStructure[newFilePath] = path.Join(f.folderStructure[folderPath], fileName)

	fs.Debugf(f, "Updated folder structure with new file: %s", newFilePath)
	return nil
}

// UploadToStorage uploads content to a specific storage location without creating an item
func (f *Fs) UploadToStorage(ctx context.Context, bucket, objectKey string, reader io.Reader, size int64) error {
	fs.Debugf(f, "UploadToStorage: Starting upload to bucket: %s, objectKey: %s, size: %d", bucket, objectKey, size)

	// Compute number of chunks (max 25)
	const maxChunks = 25
	const minChunkSize = 5 * 1024 * 1024 // 5 MB minimum chunk size

	chunkSize := size / maxChunks
	if chunkSize < minChunkSize {
		chunkSize = minChunkSize
	}
	numChunks := int(math.Ceil(float64(size) / float64(chunkSize)))
	if numChunks > maxChunks {
		numChunks = maxChunks
		chunkSize = size / int64(numChunks)
	}

	fs.Debugf(f, "UploadToStorage: Calculated %d chunks of size %d", numChunks, chunkSize)

	// Get signed upload URLs
	uploadResp, err := f.CreateSignedS3UploadLinks(ctx, bucket, objectKey, numChunks)
	if err != nil {
		return fmt.Errorf("error getting signed upload URLs: %w", err)
	}

	// Upload chunks with retry logic
	const maxRetries = 1
	const retryDelay = 2 * time.Second

	for i := 0; i < numChunks; i++ {
		chunk := make([]byte, chunkSize)
		n, err := io.ReadFull(reader, chunk)
		if err != nil && err != io.ErrUnexpectedEOF {
			return fmt.Errorf("error reading chunk %d: %w", i+1, err)
		}
		chunk = chunk[:n] // Trim chunk if it's the last one and smaller

		var uploadErr error
		for retry := 0; retry < maxRetries; retry++ {
			uploadErr = f.UploadChunk(ctx, uploadResp.URLs[i], chunk, i+1)
			if uploadErr == nil {
				break // Successful upload, exit retry loop
			}

			fs.Debugf(f, "Error uploading chunk %d (attempt %d): %v", i+1, retry+1, uploadErr)

			if retry < maxRetries-1 {
				// Wait before retrying
				select {
				case <-time.After(retryDelay):
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}

		if uploadErr != nil {
			return fmt.Errorf("failed to upload chunk %d after %d attempts: %w", i+1, maxRetries, uploadErr)
		}

		fs.Debugf(f, "Successfully uploaded chunk %d", i+1)
	}

	// Finalize upload
	err = f.FinalizeUpload(ctx, bucket, objectKey, uploadResp.UploadKey)
	if err != nil {
		return fmt.Errorf("error finalizing upload: %w", err)
	}

	fs.Debugf(f, "UploadToStorage: Successfully uploaded to storage")
	return nil
}

// CreateNewVersion creates a new version for an existing item
func (o *Object) CreateNewVersion(ctx context.Context, storageUrn, fileName string) error {
	fs.Debugf(o, "CreateNewVersion: Starting for item %s with storage URN: %s", o.id, storageUrn)

	url := fmt.Sprintf("https://developer.api.autodesk.com/data/v1/projects/%s/versions", o.fs.opt.ProjectID)

	payload := map[string]interface{}{
		"jsonapi": map[string]string{
			"version": "1.0",
		},
		"data": map[string]interface{}{
			"type": "versions",
			"attributes": map[string]interface{}{
				"name": fileName,
				"extension": map[string]interface{}{
					"type":    "versions:autodesk.bim360:File",
					"version": "1.0",
				},
			},
			"relationships": map[string]interface{}{
				"item": map[string]interface{}{
					"data": map[string]string{
						"type": "items",
						"id":   o.itemID,
					},
				},
				"storage": map[string]interface{}{
					"data": map[string]string{
						"type": "objects",
						"id":   storageUrn,
					},
				},
			},
			"meta": map[string]interface{}{
				"workflow": "customMeta",
				"workflowAttribute": map[string]string{
					"MyLastActionDate": time.Now().Format(time.RFC3339),
				},
			},
		},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		fs.Debugf(o, "CreateNewVersion: Error marshaling payload: %v", err)
		return fmt.Errorf("error marshaling payload: %w", err)
	}

	fs.Debugf(o, "CreateNewVersion: Sending POST request to %s", url)

	resp, err := o.fs.doRequest(ctx, "POST", url, bytes.NewBuffer(jsonPayload), map[string]string{
		"Content-Type": "application/vnd.api+json",
		"Accept":       "application/vnd.api+json",
	})
	if err != nil {
		fs.Debugf(o, "CreateNewVersion: Error sending request: %v", err)
		return fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		fs.Debugf(o, "CreateNewVersion: API request failed with status code: %d, body: %s", resp.StatusCode, string(body))
		return fmt.Errorf("API request failed with status code: %d, body: %s", resp.StatusCode, string(body))
	}

	// Parse the response to get the new version details
	var versionResp api.Version
	err = json.NewDecoder(resp.Body).Decode(&versionResp)
	if err != nil {
		fs.Debugf(o, "CreateNewVersion: Error decoding response: %v", err)
		return fmt.Errorf("error decoding response: %w", err)
	}

	fs.Debugf(o, "CreateNewVersion: Successfully created new version %s for item %s", versionResp.Data.ID, o.id)
	return nil
}
