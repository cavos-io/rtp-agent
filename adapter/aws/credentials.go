package aws

import (
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscredentials "github.com/aws/aws-sdk-go-v2/credentials"
)

type AWSCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

func (c AWSCredentials) valid() bool {
	return c.AccessKeyID != "" && c.SecretAccessKey != ""
}

func awsCredentialsLoadOption(creds AWSCredentials, set bool) func(*awsconfig.LoadOptions) error {
	if !set || !creds.valid() {
		return nil
	}
	return awsconfig.WithCredentialsProvider(awscredentials.NewStaticCredentialsProvider(
		creds.AccessKeyID,
		creds.SecretAccessKey,
		creds.SessionToken,
	))
}
