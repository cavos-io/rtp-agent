package google

import "google.golang.org/api/option"

func googleClientOptionsFromCredentialsFile(credentialsFile string) ([]option.ClientOption, error) {
	if credentialsFile == "" {
		return nil, nil
	}
	return []option.ClientOption{option.WithAuthCredentialsFile(option.ServiceAccount, credentialsFile)}, nil
}
