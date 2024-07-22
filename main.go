package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudflare/cloudflare-go"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	apply     bool   // Set to true for dry run, false to perform actual deletions
	apiToken  string // Cloudflare API token
	zoneName  string // Cloudflare Zone name
	inputFile string // Filename for the list of DNS records to delete

	logger *zap.Logger

	rootCmd = &cobra.Command{
		Use:   "Cloudflare Remove DNS",
		Short: "CLI tool to remove DNS records from Cloudflare",
		Long:  "A CLI tool to remove DNS records from Cloudflare based on a list of hostnames",
		Run:   run,
	}
)

func init() {
	logger = createLogger()
	defer logger.Sync()

	// Here we define our flags and configuration settings.
	rootCmd.PersistentFlags().BoolVarP(&apply, "apply", "a", false, "Apply changes")
	rootCmd.PersistentFlags().StringVarP(&apiToken, "apitoken", "t", "", "Cloudflare API token")
	rootCmd.PersistentFlags().StringVarP(&zoneName, "zonename", "z", "", "Cloudflare Zone name")
	rootCmd.PersistentFlags().StringVarP(&inputFile, "filename", "f", "hostnames.txt", "Filename for the list of DNS records to delete")

	// Mark apiToken and zoneName as required flags
	//rootCmd.MarkPersistentFlagRequired("apitoken")
	//rootCmd.MarkPersistentFlagRequired("zonename")
}

func main() {
	// Configuration via environment variables
	apiToken = os.Getenv("CLOUDFLARE_API_TOKEN")
	if apiToken == "" {
		logger.Fatal("CLOUDFLARE_API_TOKEN environment variable is not set")
	}
	zoneName = os.Getenv("CLOUDFLARE_ZONE")
	if zoneName == "" {
		logger.Fatal("CLOUDFLARE_ZONE environment variable is not set")
	}

	// Check if the filename for the list of DNS records to delete is set
	if inputFile == "" {
		// check if the file exists and is readable
		fileInfo, err := os.Stat(inputFile)
		if os.IsNotExist(err) {
			logger.Fatal("File does not exist", zap.String("filename", inputFile))
		}
		if fileInfo.Mode().IsDir() {
			logger.Fatal("Path is a directory, not a file", zap.String("filename", inputFile))
		}
		if fileInfo.Mode().Perm()&0400 == 0 {
			logger.Fatal("File is not readable", zap.String("filename", inputFile))
		}
		logger.Fatal("Filename for the list of DNS records to delete is not set. Default filename: hostnames.txt", zap.String("filename", inputFile))
	}

	if err := rootCmd.Execute(); err != nil {
		logger.Fatal("Error: %v", zap.Error(err))
	}
}

func run(cmd *cobra.Command, args []string) {
	// Read the list of hostnames from the input file
	hostnames, err := readInputFile(inputFile)
	if err != nil {
		logger.Fatal("Failed to read file", zap.String("filename", inputFile), zap.Error(err))
	}

	// Create a new API instance
	api, err := cloudflare.NewWithAPIToken(apiToken)
	if err != nil {
		logger.Fatal("Failed to create API instance", zap.Error(err))
	}

	// Fetch the zone ID using the zone name
	zoneID, err := api.ZoneIDByName(zoneName)
	if err != nil {
		logger.Fatal("Failed to fetch zone ID", zap.String("zoneName", zoneName), zap.Error(err))
	}

	// Iterate over the list of hostnames and delete each record
	for _, hostname := range hostnames {
		records, err := fetchDNSRecords(api, zoneID, hostname)
		if err != nil {
			continue
		}
		for _, record := range records {
			err = deleteDNSRecord(api, record)
			if err != nil {
				logger.Error("Error deleting record", zap.Error(err))
			}
		}
	}
}

func createLogger() *zap.Logger {
	stdout := zapcore.AddSync(os.Stdout)

	logFile, err := os.OpenFile("log.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(fmt.Sprintf("Failed to open log file: %v", err))
	}

	file := zapcore.AddSync(logFile)

	level := zap.NewAtomicLevelAt(zap.InfoLevel)

	productionCfg := zap.NewProductionEncoderConfig()
	productionCfg.TimeKey = "timestamp"
	productionCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	developmentCfg := zap.NewDevelopmentEncoderConfig()
	developmentCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder

	consoleEncoder := zapcore.NewConsoleEncoder(developmentCfg)
	fileEncoder := zapcore.NewJSONEncoder(productionCfg)

	core := zapcore.NewTee(
		zapcore.NewCore(consoleEncoder, stdout, level),
		zapcore.NewCore(fileEncoder, file, level),
	)
	return zap.New(core)
}

// Function to fetch DNS records for a given hostname
func fetchDNSRecords(api *cloudflare.API, zoneID, hostname string) ([]cloudflare.DNSRecord, error) {
	logger.Info("Fetching Record: ", zap.String("hostname", hostname))
	records, _, err := api.ListDNSRecords(context.Background(), cloudflare.ZoneIdentifier(zoneID), cloudflare.ListDNSRecordsParams{Name: hostname})
	if err != nil {
		logger.Error("Failed to fetch DNS record", zap.String("hostname", hostname), zap.Error(err))
		return nil, err
	}
	if len(records) == 0 {
		logger.Info("No records found for", zap.String("hostname", hostname))
	}
	return records, nil
}

// Function to delete a DNS record
func deleteDNSRecord(api *cloudflare.API, record cloudflare.DNSRecord) error {
	if !apply {
		logger.Info("[DRY RUN] Deleting Record: ", zap.String("recordID", record.ID), zap.String("zoneID", record.ZoneID), zap.String("name", record.Name), zap.String("type", record.Type), zap.String("content", record.Content))
		return nil
	}
	err := api.DeleteDNSRecord(context.Background(), cloudflare.ZoneIdentifier(record.ZoneID), record.ID)
	if err != nil {
		logger.Error("Failed to delete DNS record", zap.String("recordID", record.ID), zap.String("name", record.Name), zap.Error(err))
		return err
	}
	return nil
}

// readInputFile reads the input file, skipping empty lines and comments.
func readInputFile(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var hostnames []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		// trim leading and trailing spaces
		line := strings.TrimSpace(scanner.Text())
		// skip comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		hostnames = append(hostnames, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return hostnames, nil
}
