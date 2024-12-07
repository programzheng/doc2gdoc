package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
)

// Config structure for storing credential information
type Config struct {
	CredentialsFile string
	TokenFile       string
}

// Initialize Google Drive client
func initClient(config Config) (*drive.Service, error) {
	b, err := os.ReadFile(config.CredentialsFile)
	if err != nil {
		return nil, fmt.Errorf("unable to read credentials file: %v", err)
	}

	// Configure credentials
	oauthConfig, err := google.ConfigFromJSON(b, drive.DriveFileScope)
	if err != nil {
		return nil, fmt.Errorf("unable to parse credentials: %v", err)
	}

	// Read or generate token
	client, err := getClient(oauthConfig, config.TokenFile)
	if err != nil {
		return nil, fmt.Errorf("unable to get client: %v", err)
	}

	// Create Drive service
	srv, err := drive.New(client)
	if err != nil {
		return nil, fmt.Errorf("unable to create Drive service: %v", err)
	}

	return srv, nil
}

// tokenFromFile reads token from file
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// saveToken saves token to file
func saveToken(path string, token *oauth2.Token) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("unable to create token file: %v", err)
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(token)
}

// getTokenFromWeb gets new token from web
func getTokenFromWeb(config *oauth2.Config) (*oauth2.Token, error) {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Please visit this URL and authorize the application:\n%v\n", authURL)
	fmt.Print("Enter the authorization code: ")

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		return nil, fmt.Errorf("unable to read authorization code: %v", err)
	}

	tok, err := config.Exchange(context.Background(), authCode)
	if err != nil {
		return nil, fmt.Errorf("unable to exchange token: %v", err)
	}
	return tok, nil
}

// Get OAuth2 client
func getClient(config *oauth2.Config, tokenFile string) (*http.Client, error) {
	tok, err := tokenFromFile(tokenFile)
	if err != nil {
		tok, err = getTokenFromWeb(config)
		if err != nil {
			return nil, err
		}
		if err := saveToken(tokenFile, tok); err != nil {
			return nil, err
		}
	}
	return config.Client(context.Background(), tok), nil
}

// Convert file to Google Docs
func convertToGoogleDocs(srv *drive.Service, filePath string, drivePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("unable to open file: %v", err)
	}
	defer file.Close()

	// Get or create target folder
	parentID, err := findOrCreateFolder(srv, drivePath)
	if err != nil {
		return fmt.Errorf("unable to process target folder: %v", err)
	}

	filename := filepath.Base(filePath)
	f := &drive.File{
		Name:     filename,
		MimeType: "application/vnd.google-apps.document",
		Parents:  []string{parentID},
	}

	res, err := srv.Files.Create(f).Media(file).Do()
	if err != nil {
		return fmt.Errorf("unable to upload file: %v", err)
	}

	fmt.Printf("Successfully converted %s to Google Docs\n", filename)
	fmt.Printf("File ID: %s\n", res.Id)
	fmt.Printf("Location: Google Drive:%s/%s\n", drivePath, filename)
	return nil
}

func findOrCreateFolder(srv *drive.Service, folderPath string) (string, error) {
	if folderPath == "" || folderPath == "/" {
		return "root", nil
	}

	folders := strings.Split(strings.Trim(folderPath, "/"), "/")
	parentID := "root"

	for _, folderName := range folders {
		// Modify query conditions, remove single quotes to avoid special character issues
		query := fmt.Sprintf(`name = "%s" and mimeType = "application/vnd.google-apps.folder" and parents in "%s" and trashed = false`,
			folderName, parentID)

		// Add error handling and logging
		fmt.Printf("Searching folder: %s\n", folderName)

		files, err := srv.Files.List().
			Q(query).
			Fields("files(id, name)").
			Do()
		if err != nil {
			return "", fmt.Errorf("unable to search folder: %v", err)
		}

		// Add logging to view search results
		fmt.Printf("Found %d matching folders\n", len(files.Files))

		if len(files.Files) > 0 {
			parentID = files.Files[0].Id
			fmt.Printf("Using existing folder ID: %s\n", parentID)
			continue
		}

		// If folder doesn't exist, create it
		folder := &drive.File{
			Name:     folderName,
			MimeType: "application/vnd.google-apps.folder",
			Parents:  []string{parentID},
		}

		createdFolder, err := srv.Files.Create(folder).Fields("id").Do()
		if err != nil {
			return "", fmt.Errorf("unable to create folder %s: %v", folderName, err)
		}

		parentID = createdFolder.Id
		fmt.Printf("Created new folder ID: %s\n", parentID)
	}

	return parentID, nil
}

// Add a helper function to list all folders under specified folder
func listFolders(srv *drive.Service, parentID string) error {
	query := fmt.Sprintf(`mimeType = "application/vnd.google-apps.folder" and parents in "%s" and trashed = false`, parentID)

	files, err := srv.Files.List().
		Q(query).
		Fields("files(id, name)").
		Do()
	if err != nil {
		return fmt.Errorf("unable to list folders: %v", err)
	}

	fmt.Println("Existing folder list:")
	for _, file := range files.Files {
		fmt.Printf("- %s (ID: %s)\n", file.Name, file.Id)
	}

	return nil
}

func main() {
	var (
		drivePath = flag.String("path", "", "Target path on Google Drive (e.g.: /documents/project)")
		listOnly  = flag.Bool("list", false, "Only list folders under target path")
	)
	flag.Parse()

	config := Config{
		CredentialsFile: "credentials.json",
		TokenFile:       "token.json",
	}

	srv, err := initClient(config)
	if err != nil {
		log.Fatalf("Unable to initialize client: %v", err)
	}

	// If in list mode, only list folders
	if *listOnly {
		parentID, err := findOrCreateFolder(srv, *drivePath)
		if err != nil {
			log.Fatalf("Unable to find target path: %v", err)
		}
		if err := listFolders(srv, parentID); err != nil {
			log.Fatalf("Unable to list folders: %v", err)
		}
		return
	}

	args := flag.Args()
	if len(args) < 1 {
		log.Fatal("Please specify the file path to convert")
	}

	err = convertToGoogleDocs(srv, args[0], *drivePath)
	if err != nil {
		log.Fatalf("Conversion failed: %v", err)
	}
}
