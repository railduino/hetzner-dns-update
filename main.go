package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	APIToken string     `json:"api_token"`
	Records  []string   `json:"records"`
	TTL      int        `json:"ttl"`
	SMTP     SMTPConfig `json:"smtp"`
	Logfile  string     `json:"logfile"`
}

type SMTPConfig struct {
	Server    string `json:"server"`
	Port      string `json:"port"`
	User      string `json:"user"`
	Password  string `json:"password"`
	Recipient string `json:"recipient"`
}

type Zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Record struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ZonesResponse struct {
	Zones []Zone `json:"zones"`
}

type RecordsResponse struct {
	Records []Record `json:"records"`
}

const hetznerAPI = "https://dns.hetzner.com/api/v1"

var config Config

func main() {
	updateMode := flag.Bool("update", false, "update A/AAAA records")
	verboseMode := flag.Bool("verbose", false, "show progress")
	flag.Parse()

	err := loadConfig("config.json")
	if err != nil {
		fmt.Println("error loading config file:", err)
		os.Exit(1)
	}

	log_name := "hetzner-dns-update.log"
	if os.Geteuid() == 0 {
		log_name = config.Logfile
	}
	log_file, err := os.OpenFile(log_name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("error opening log file:", err)
		os.Exit(1)
	}
	defer log_file.Close()
	log.SetOutput(log_file)

	ipv4, ipv6, err := getPublicIPs()
	if err != nil {
		logAndMail("error getting current public IP: " + err.Error())
		os.Exit(1)
	}
	log.Printf("Current public IP: '%s' / '%s'\n", ipv4, ipv6)

	for _, fullDomain := range config.Records {
		if *verboseMode {
			fmt.Println("processing record:", fullDomain)
		}
		parts := strings.SplitN(fullDomain, ".", 2)
		if len(parts) != 2 {
			logAndMail("invalid domain name: " + fullDomain)
			continue
		}
		namePart := parts[0]
		zonePart := parts[1]

		zoneID, err := findZoneID(zonePart)
		if err != nil {
			logAndMail("error fetching zone ID: " + err.Error())
			continue
		}

		recordA, recordAAAA, err := findRecords(zoneID, namePart)
		if err != nil {
			logAndMail("error fetching A/AAAA records: " + err.Error())
			continue
		}

		//
		// Handle IPv4
		//
		if ipv4 != "" {
			if recordA.Value != "" {
				// Case: cur+ / rec+
				if recordA.Value == ipv4 {
					if *verboseMode {
						fmt.Println("- A record is current for:", fullDomain)
					}
				} else {
					if *verboseMode {
						fmt.Println("- A record needs update for:", fullDomain)
					}
					if *updateMode {
						err = updateRecord(zoneID, recordA.ID, "A", namePart, ipv4)
						if err != nil {
							logAndMail("error updating A record: " + err.Error())
						} else {
							logAndMail("A record was updated: " + fullDomain)
						}
					}
				}
			} else {
				// Case: cur+ / rec-
				if *verboseMode {
					fmt.Println("- A record needs create for:", fullDomain)
				}
				if *updateMode {
					err = createRecord(zoneID, "A", namePart, ipv4)
					if err != nil {
						logAndMail("error creating A record: " + err.Error())
					} else {
						logAndMail("A record was created: " + fullDomain)
					}
				}
			}
		} else {
			if recordA.Value != "" {
				// Case: cur- / rec+
				if *verboseMode {
					fmt.Println("- A record needs delete for:", fullDomain)
				}
				if *updateMode {
					err = deleteRecord(recordA.ID)
					if err != nil {
						logAndMail("error deleting A record: " + err.Error())
					} else {
						logAndMail("A record was deleted: " + fullDomain)
					}
				}
			} else {
				// Case: cur- / rec-
				if *verboseMode {
					fmt.Println("- no need for A record for:", fullDomain)
				}
			}
		}

		//
		// Handle IPv6
		//
		if ipv6 != "" {
			if recordAAAA.Value != "" {
				// Case: cur+ / rec+
				if recordAAAA.Value == ipv6 {
					if *verboseMode {
						fmt.Println("- AAAA record is current for:", fullDomain)
					}
				} else {
					if *verboseMode {
						fmt.Println("- AAAA record needs update for:", fullDomain)
					}
					if *updateMode {
						err = updateRecord(zoneID, recordAAAA.ID, "AAAA", namePart, ipv6)
						if err != nil {
							logAndMail("error updating AAAA record: " + err.Error())
						} else {
							logAndMail("AAAA record was updated: " + fullDomain)
						}
					}
				}
			} else {
				// Case: cur+ / rec-
				if *verboseMode {
					fmt.Println("- AAAA record needs create for:", fullDomain)
				}
				if *updateMode {
					err = createRecord(zoneID, "AAAA", namePart, ipv6)
					if err != nil {
						logAndMail("error creating AAAA record: " + err.Error())
					} else {
						logAndMail("AAAA record was created: " + fullDomain)
					}
				}
			}
		} else {
			if recordAAAA.Value != "" {
				// Case: cur- / rec+
				if *verboseMode {
					fmt.Println("- AAAA record needs delete for:", fullDomain)
				}
				if *updateMode {
					err = deleteRecord(recordAAAA.ID)
					if err != nil {
						logAndMail("error deleting AAAA record: " + err.Error())
					} else {
						logAndMail("AAAA record was deleted: " + fullDomain)
					}
				}
			} else {
				// Case: cur- / rec-
				if *verboseMode {
					fmt.Println("- no need for AAAA record for:", fullDomain)
				}
			}
		}
	}
}

func loadConfig(filename string) error {
	config_dir, _ := os.Getwd()
	if snap_dir := os.Getenv("SNAP_USER_COMMON"); snap_dir != "" {
		config_dir = snap_dir
	} else if env_dir := os.Getenv("CONFIG_DIR"); env_dir != "" {
		config_dir = env_dir
	}

	config_file := filepath.Join(config_dir, filename)
	data, err := os.ReadFile(config_file)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &config)
}

func getPublicIPs() (string, string, error) {
	resp4, err := http.Get("https://api.ipify.org")
	if err != nil {
		return "", "", err
	}
	defer resp4.Body.Close()
	ip4, err := io.ReadAll(resp4.Body)
	if err != nil {
		return "", "", err
	}

	resp6, err := http.Get("https://api6.ipify.org")
	if err != nil {
		return string(ip4), "", nil
	}
	defer resp6.Body.Close()
	ip6, err := io.ReadAll(resp6.Body)
	if err != nil {
		return "", "", err
	}

	return string(ip4), string(ip6), nil
}

func findZoneID(domain string) (string, error) {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", hetznerAPI+"/zones", nil)
	req.Header.Add("Auth-API-Token", config.APIToken)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var zones ZonesResponse
	decoder := json.NewDecoder(resp.Body)
	decoder.Decode(&zones)

	for _, zone := range zones.Zones {
		if zone.Name == domain {
			return zone.ID, nil
		}
	}
	return "", fmt.Errorf("can't find domain '%s'", domain)
}

func findRecords(zoneID, fullDomain string) (Record, Record, error) {
	recordA := Record{}
	recordAAAA := Record{}

	client := &http.Client{}
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/records?zone_id=%s", hetznerAPI, zoneID), nil)
	req.Header.Add("Auth-API-Token", config.APIToken)
	resp, err := client.Do(req)
	if err != nil {
		return recordA, recordAAAA, err
	}
	defer resp.Body.Close()

	var records RecordsResponse
	decoder := json.NewDecoder(resp.Body)
	decoder.Decode(&records)

	for _, rec := range records.Records {
		if rec.Name == fullDomain && rec.Type == "A" {
			recordA = rec
			continue
		}
		if rec.Name == fullDomain && rec.Type == "AAAA" {
			recordAAAA = rec
			continue
		}
	}

	if recordA.Type == "" && recordAAAA.Type == "" {
		return recordA, recordAAAA, fmt.Errorf("can't find A record for '%s'", fullDomain)
	}
	return recordA, recordAAAA, nil
}

func createRecord(zoneID, recType, name, newIP string) error {
	client := &http.Client{}
	payload := map[string]interface{}{
		"zone_id": zoneID,
		"type":    recType,
		"name":    name,
		"value":   newIP,
		"ttl":     config.TTL,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", fmt.Sprintf("%s/records", hetznerAPI), bytes.NewBuffer(body))
	req.Header.Add("Auth-API-Token", config.APIToken)
	req.Header.Add("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("create status: %s", resp.Status)
	}
	return nil
}

func updateRecord(zoneID, recordID, recType, name, newIP string) error {
	client := &http.Client{}
	payload := map[string]interface{}{
		"zone_id": zoneID,
		"type":    recType,
		"name":    name,
		"value":   newIP,
		"ttl":     config.TTL,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/records/%s", hetznerAPI, recordID), bytes.NewBuffer(body))
	req.Header.Add("Auth-API-Token", config.APIToken)
	req.Header.Add("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("update status: %s", resp.Status)
	}
	return nil
}

func deleteRecord(recordID string) error {
	client := &http.Client{}
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("%s/records/%s", hetznerAPI, recordID), nil)
	req.Header.Add("Auth-API-Token", config.APIToken)
	req.Header.Add("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("delete status: %s", resp.Status)
	}
	return nil
}

func logAndMail(message string) {
	log.Println(message)
	sendEmail("DNS Update Status", message)
}

func sendEmail(subject, body string) {
	auth := smtp.PlainAuth("", config.SMTP.User, config.SMTP.Password, config.SMTP.Server)
	msg := []byte("From: " + config.SMTP.User + "\r\n" +
		"To: " + config.SMTP.Recipient + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		body + "\r\n")
	err := smtp.SendMail(config.SMTP.Server+":"+config.SMTP.Port, auth, config.SMTP.User, []string{config.SMTP.Recipient}, msg)
	if err != nil {
		log.Println("eror sending email:", err)
	}
}
