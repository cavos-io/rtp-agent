#!/bin/bash

set -e  # Exit immediately if a command fails

# Check if GITHUB_ACCESS_TOKEN is set
if [[ -z "$GITHUB_ACCESS_TOKEN" ]]; then
    echo "Error: GITHUB_ACCESS_TOKEN is not set. Please export it before running this script."
    exit 1
fi

# Define GitLab domain (modify if needed)
GITHUB_DOMAIN="github.com"

echo "Configuring Git credentials for private repositories..."

# Allow Go to access private modules
export GOPRIVATE="$GITHUB_DOMAIN/*"

# Set up Git authentication for Go Modules
export GIT_ASKPASS=$(mktemp)
echo "echo 'username=oauth2'" > "$GIT_ASKPASS"
echo "echo 'password=$GITHUB_ACCESS_TOKEN'" >> "$GIT_ASKPASS"
chmod +x "$GIT_ASKPASS"

# Tell Git to use the access token
git config --global credential.helper cache
git config --global url."https://oauth2:$GITHUB_ACCESS_TOKEN@$GITHUB_DOMAIN".insteadOf "https://$GITHUB_DOMAIN"

echo "Git credentials configured successfully."

# Remove go.mod and go.sum if they exist
rm -f go.mod go.sum

# Initialize a new Go module if go.mod does not exist
if [ ! -f go.mod ]; then
    echo "Initializing a new Go module"
    go mod init github.com/cavos-io/conversation-worker
fi

# Fetch dependencies
echo "Fetching dependencies..."
go list -m all | tail -n +2 | awk '{print $1}' | while read -r module; do
    echo "Fetching $module"
    go get "$module"
done

# Tidy up the go.mod and go.sum files
echo "Tidying go.mod and go.sum..."
go mod tidy

# Cleanup GIT_ASKPASS
rm -f "$GIT_ASKPASS"

echo "All dependencies have been fetched and go.mod/go.sum have been updated."
