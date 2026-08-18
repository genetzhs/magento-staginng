package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// credentials is the JSON document stored at
// /var/www/vhosts/<domain>/.credentials/<staging-name>.json (chmod 0400).
type credentials struct {
	Domain            string            `json:"domain"`
	StagingName       string            `json:"staging_name"`
	StagingURL        string            `json:"staging_url"`
	AdminPath         string            `json:"admin_path,omitempty"`
	SourcePath        string            `json:"source_path"`
	TargetPath        string            `json:"target_path"`
	SourceDB          string            `json:"source_db"`
	TargetDB          dbCreds           `json:"target_db"`
	RedisIDPrefix     string            `json:"redis_id_prefix"`
	ElasticSuffix     string            `json:"elastic_suffix"`
	ElasticPrefixes   elasticPrefixes   `json:"elastic_prefixes"`
	BasicAuth         basicAuth         `json:"basic_auth,omitempty"`
	CreatedAt         string            `json:"created_at"`
	MagentoMode       string            `json:"magento_mode,omitempty"`
	BinaryVersion     string            `json:"binary_version"`
}

type dbCreds struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
}

type elasticPrefixes struct {
	ES6     string `json:"elasticsearch6,omitempty"`
	ES7     string `json:"elasticsearch7,omitempty"`
	Amasty  string `json:"amasty_elastic,omitempty"`
}

type basicAuth struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

// saveCredentials writes the credentials JSON to disk with mode 0400.
func saveCredentials(c *config) error {
	if c.dryRun {
		infof("  [dry-run] write credentials to %s", c.credsPath)
		return nil
	}

	if err := ensureCredsDir(c); err != nil {
		return err
	}

	creds := credentials{
		Domain:      c.domain,
		StagingName: c.stagingName,
		StagingURL:  c.stagingURL(),
		AdminPath:   c.sourceAdminFrontName,
		SourcePath:  c.sourcePath,
		TargetPath:  c.targetPath,
		SourceDB:    c.sourceDB,
		TargetDB: dbCreds{
			Name:     c.targetDB,
			User:     c.targetDBUser,
			Password: c.targetDBPass,
		},
		RedisIDPrefix: c.redisIDPrefix,
		ElasticSuffix: c.elasticSuffix,
		ElasticPrefixes: elasticPrefixes{
			ES6:    c.originalES6Prefix + c.elasticSuffix,
			ES7:    c.originalES7Prefix + c.elasticSuffix,
			Amasty: c.originalAmastyPrefix + c.elasticSuffix,
		},
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		MagentoMode:   c.magentoMode,
		BinaryVersion: version,
	}

	if !c.skipBasicAuth {
		creds.BasicAuth = basicAuth{
			User:     c.basicAuthUser,
			Password: c.basicAuthPass,
		}
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %v", err)
	}
	if err := os.WriteFile(c.credsPath, data, 0400); err != nil {
		return fmt.Errorf("failed to write credentials: %v", err)
	}
	if err := os.Chmod(c.credsPath, 0400); err != nil {
		warnf("chmod credentials failed: %v", err)
	}
	return nil
}

// loadCredentials reads a previously-saved credentials file.
func loadCredentials(path string) (*credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
