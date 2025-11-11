package api

import (
	"time"
)

type Hub struct {
	Jsonapi struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Links struct {
		Self struct {
			Href    string `json:"href"`
			Related struct {
				Href string `json:"href"`
			} `json:"related"`
		} `json:"self"`
	} `json:"links"`
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Name      string `json:"name"`
			Extension struct {
				Data    map[string]interface{} `json:"data"`
				Version string                 `json:"version"`
				Type    string                 `json:"type"`
				Schema  struct {
					Href string `json:"href"`
				} `json:"schema"`
			} `json:"extension"`
			Region string `json:"region"`
		} `json:"attributes"`
		Relationships struct {
			Projects struct {
				Links struct {
					Related struct {
						Href string `json:"href"`
					} `json:"related"`
				} `json:"links"`
			} `json:"projects"`
			PimCollection struct {
				Data struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
			} `json:"pimCollection"`
		} `json:"relationships"`
		Links struct {
			Self struct {
				Href string `json:"href"`
			} `json:"self"`
		} `json:"links"`
	} `json:"data"`
}

type FoldersResponse struct {
	Jsonapi struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Links struct {
		Self struct {
			Href string `json:"href"`
		} `json:"self"`
	} `json:"links"`
	Data []Folder `json:"data"`
}

type Folder struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Attributes struct {
		Name                   string    `json:"name"`
		DisplayName            string    `json:"displayName"`
		CreateTime             time.Time `json:"createTime"`
		CreateUserId           string    `json:"createUserId"`
		CreateUserName         string    `json:"createUserName"`
		LastModifiedTime       time.Time `json:"lastModifiedTime"`
		LastModifiedUserId     string    `json:"lastModifiedUserId"`
		LastModifiedUserName   string    `json:"lastModifiedUserName"`
		LastModifiedTimeRollup time.Time `json:"lastModifiedTimeRollup"`
		ObjectCount            int       `json:"objectCount"`
		Hidden                 bool      `json:"hidden"`
		Extension              struct {
			Type    string `json:"type"`
			Version string `json:"version"`
			Schema  struct {
				Href string `json:"href"`
			} `json:"schema"`
			Data struct {
				AllowedTypes  []string `json:"allowedTypes"`
				VisibleTypes  []string `json:"visibleTypes"`
				IsRoot        bool     `json:"isRoot"`
				FolderType    string   `json:"folderType"`
				FolderParents []struct {
					Urn       string `json:"urn"`
					IsRoot    bool   `json:"isRoot"`
					Title     string `json:"title"`
					ParentUrn string `json:"parentUrn"`
				} `json:"folderParents"`
				NamingStandardIds []string `json:"namingStandardIds"`
			} `json:"data"`
		} `json:"extension"`
	} `json:"attributes"`
	Links struct {
		Self struct {
			Href string `json:"href"`
		} `json:"self"`
		WebView struct {
			Href string `json:"href"`
		} `json:"webView"`
	} `json:"links"`
	Relationships struct {
		Parent struct {
			Links struct {
				Related struct {
					Href string `json:"href"`
				} `json:"related"`
			} `json:"links"`
			Data struct {
				Type string `json:"type"`
				ID   string `json:"id"`
			} `json:"data"`
		} `json:"parent"`
		Refs struct {
			Links struct {
				Self struct {
					Href string `json:"href"`
				} `json:"self"`
				Related struct {
					Href string `json:"href"`
				} `json:"related"`
			} `json:"links"`
		} `json:"refs"`
		Links struct {
			Links struct {
				Self struct {
					Href string `json:"href"`
				} `json:"self"`
			} `json:"links"`
		} `json:"links"`
		Contents struct {
			Links struct {
				Related struct {
					Href string `json:"href"`
				} `json:"related"`
			} `json:"links"`
		} `json:"contents"`
	} `json:"relationships"`
}

type Item struct {
	Jsonapi struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Links struct {
		Self struct {
			Href string `json:"href"`
		} `json:"self"`
	} `json:"links"`
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			DisplayName          string    `json:"displayName"`
			CreateTime           time.Time `json:"createTime"`
			CreateUserId         string    `json:"createUserId"`
			CreateUserName       string    `json:"createUserName"`
			LastModifiedTime     time.Time `json:"lastModifiedTime"`
			LastModifiedUserId   string    `json:"lastModifiedUserId"`
			LastModifiedUserName string    `json:"lastModifiedUserName"`
			Hidden               bool      `json:"hidden"`
			Reserved             bool      `json:"reserved"`
			Extension            struct {
				Type    string `json:"type"`
				Version string `json:"version"`
				Schema  struct {
					Href string `json:"href"`
				} `json:"schema"`
				Data struct {
					SourceFileName string `json:"sourceFileName"`
				} `json:"data"`
			} `json:"extension"`
		} `json:"attributes"`
		Links struct {
			Self struct {
				Href string `json:"href"`
			} `json:"self"`
			WebView struct {
				Href string `json:"href"`
			} `json:"webView"`
		} `json:"links"`
		Relationships struct {
			Tip struct {
				Data struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
				Links struct {
					Related struct {
						Href string `json:"href"`
					} `json:"related"`
				} `json:"links"`
			} `json:"tip"`
			Versions struct {
				Links struct {
					Related struct {
						Href string `json:"href"`
					} `json:"related"`
				} `json:"links"`
			} `json:"versions"`
			Parent struct {
				Data struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
				Links struct {
					Related struct {
						Href string `json:"href"`
					} `json:"related"`
				} `json:"links"`
			} `json:"parent"`
			Refs  RelationshipLinks `json:"refs"`
			Links RelationshipLinks `json:"links"`
		} `json:"relationships"`
	} `json:"data"`
	Included []Version `json:"included"`
}

type RelationshipLinks struct {
	Links struct {
		Self struct {
			Href string `json:"href"`
		} `json:"self"`
		Related *struct {
			Href string `json:"href"`
		} `json:"related,omitempty"`
	} `json:"links"`
}

type Version struct {
	Jsonapi struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Links struct {
		Self struct {
			Href string `json:"href"`
		} `json:"self"`
	} `json:"links"`
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Name                 string    `json:"name"`
			DisplayName          string    `json:"displayName"`
			CreateTime           time.Time `json:"createTime"`
			CreateUserId         string    `json:"createUserId"`
			CreateUserName       string    `json:"createUserName"`
			LastModifiedTime     time.Time `json:"lastModifiedTime"`
			LastModifiedUserId   string    `json:"lastModifiedUserId"`
			LastModifiedUserName string    `json:"lastModifiedUserName"`
			VersionNumber        int       `json:"versionNumber"`
			MimeType             string    `json:"mimeType"`
			Extension            struct {
				Type    string `json:"type"`
				Version string `json:"version"`
				Schema  struct {
					Href string `json:"href"`
				} `json:"schema"`
				Data struct {
					TempUrn          interface{}            `json:"tempUrn"`
					Properties       map[string]interface{} `json:"properties"`
					StorageUrn       string                 `json:"storageUrn"`
					StorageType      string                 `json:"storageType"`
					ConformingStatus string                 `json:"conformingStatus"`
				} `json:"data"`
			} `json:"extension"`
		} `json:"attributes"`
		Links struct {
			Self struct {
				Href string `json:"href"`
			} `json:"self"`
			WebView struct {
				Href string `json:"href"`
			} `json:"webView"`
		} `json:"links"`
		Relationships struct {
			Item struct {
				Links struct {
					Related struct {
						Href string `json:"href"`
					} `json:"related"`
				} `json:"links"`
				Data struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
			} `json:"item"`
			Refs            RelationshipLinks        `json:"refs"`
			Links           RelationshipLinks        `json:"links"`
			Storage         RelationshipDataWithMeta `json:"storage"`
			Derivatives     RelationshipDataWithMeta `json:"derivatives"`
			Thumbnails      RelationshipDataWithMeta `json:"thumbnails"`
			DownloadFormats struct {
				Links struct {
					Related struct {
						Href string `json:"href"`
					} `json:"related"`
				} `json:"links"`
			} `json:"downloadFormats"`
		} `json:"relationships"`
	} `json:"data"`
}

type RelationshipDataWithMeta struct {
	Data struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"data"`
	Meta struct {
		Link struct {
			Href string `json:"href"`
		} `json:"link"`
	} `json:"meta"`
}

// Add this new type to the existing file

type FolderContentsResponse struct {
	Jsonapi struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Links struct {
		Self  Link `json:"self"`
		First Link `json:"first"`
		Prev  Link `json:"prev"`
		Next  Link `json:"next"`
	} `json:"links"`
	Data     []interface{} `json:"data"` // This can be either Folder or Item
	Included []Version     `json:"included"`
}

type Link struct {
	Href string `json:"href"`
}

// Ensure the existing Folder and Item types are up to date
// If they're missing any fields from the folder contents response, add them here

// TokenResponse represents the response from the token refresh endpoint
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// ObjectDetails represents the details of an object in Autodesk Construction Cloud
type ObjectDetails struct {
	BucketKey   string   `json:"bucketKey"`
	ObjectID    string   `json:"objectId"`
	ObjectKey   string   `json:"objectKey"`
	SHA1        string   `json:"sha1"`
	Size        int64    `json:"size"`
	ContentType string   `json:"contentType"`
	Location    string   `json:"location"`
	BlockSizes  []int64  `json:"blockSizes"`
	Deltas      []string `json:"deltas"` // This is an empty array in the example, so we'll use []string for flexibility
}

// SignedS3DownloadResponse represents the response from the signed S3 download API
type SignedS3DownloadResponse struct {
	Status string `json:"status"`
	URL    string `json:"url"`
	Params struct {
		ContentType        string `json:"content-type"`
		ContentDisposition string `json:"content-disposition"`
	} `json:"params"`
	Size int64  `json:"size"`
	SHA1 string `json:"sha1"`
}

// SignedS3UploadResponse represents the response for a signed S3 upload
type SignedS3UploadResponse struct {
	UploadKey        string    `json:"uploadKey"`
	UploadExpiration time.Time `json:"uploadExpiration"`
	URLExpiration    time.Time `json:"urlExpiration"`
	URLs             []string  `json:"urls"`
}

type StorageLocationResponse struct {
	Jsonapi struct {
		Version string `json:"version"`
	} `json:"jsonapi"`
	Links struct {
		Self struct {
			Href string `json:"href"`
		} `json:"self"`
	} `json:"links"`
	Data struct {
		Type          string `json:"type"`
		ID            string `json:"id"`
		Relationships struct {
			Target struct {
				Links struct {
					Related struct {
						Href string `json:"href"`
					} `json:"related"`
				} `json:"links"`
				Data struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
			} `json:"target"`
		} `json:"relationships"`
	} `json:"data"`
}
