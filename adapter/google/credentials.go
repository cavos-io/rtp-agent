package google

import (
	"encoding/json"
	"os"

	"google.golang.org/api/option"
)

func googleClientOptionsFromCredentialsFile(credentialsFile string) ([]option.ClientOption, error) {
	if credentialsFile == "" {
		return nil, nil
	}
	return []option.ClientOption{option.WithAuthCredentialsFile(option.ServiceAccount, credentialsFile)}, nil
}

func googleProjectFromCredentialsFile(credentialsFile string) (string, error) {
	if credentialsFile == "" {
		return "", nil
	}
	data, err := os.ReadFile(credentialsFile)
	if err != nil {
		return "", err
	}
	var credentials struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(data, &credentials); err != nil {
		return "", err
	}
	return credentials.ProjectID, nil
}

func googleProjectFromADCEnv() string {
	if project := os.Getenv("GOOGLE_CLOUD_PROJECT"); project != "" {
		return project
	}
	return os.Getenv("GCLOUD_PROJECT")
}
