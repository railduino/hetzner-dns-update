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
	updateMode := flag.Bool("update", false, "A-Record wirklich aktualisieren")
	flag.Parse()

	err := loadConfig("config.json")
	if err != nil {
		fmt.Println("Fehler beim Laden der Konfiguration:", err)
		os.Exit(1)
	}

	logFile, err := os.OpenFile(config.Logfile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("Fehler beim Öffnen der Logdatei:", err)
		os.Exit(1)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	ipv4, err := getPublicIP()
	if err != nil {
		logAndMail("Fehler beim Ermitteln der IPv4: " + err.Error())
		os.Exit(1)
	}
	log.Println("Aktuelle öffentliche IPv4:", ipv4)

	for _, fullDomain := range config.Records {
		log.Println("Bearbeite Record:", fullDomain)
		zoneID, err := findZoneID(fullDomain)
		if err != nil {
			logAndMail("Zone-ID Fehler: " + err.Error())
			continue
		}

		parts := strings.Split(fullDomain, ".")
		record, err := findARecord(zoneID, parts[0])
		if err != nil {
			logAndMail("Record Fehler: " + err.Error())
			continue
		}

		if record.Value == ipv4 {
			log.Println("Keine Aktualisierung nötig für", fullDomain)
			continue
		}

		if *updateMode {
			err = updateRecord(zoneID, record.ID, parts[0], ipv4)
			if err != nil {
				logAndMail("Update Fehler: " + err.Error())
			} else {
				logAndMail("Erfolgreich aktualisiert: " + fullDomain)
			}
		}
	}
}

func loadConfig(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &config)
}

func getPublicIP() (string, error) {
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	ip, err := io.ReadAll(resp.Body)
	return string(ip), err
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

	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("ungültiger Domainname: %s", domain)
	}
	baseDomain := parts[len(parts)-2] + "." + parts[len(parts)-1]

	for _, zone := range zones.Zones {
		if zone.Name == baseDomain {
			return zone.ID, nil
		}
	}
	return "", fmt.Errorf("Zone nicht gefunden für Domain: %s", domain)
}

func findARecord(zoneID, fullDomain string) (Record, error) {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/records?zone_id=%s", hetznerAPI, zoneID), nil)
	req.Header.Add("Auth-API-Token", config.APIToken)
	resp, err := client.Do(req)
	if err != nil {
		return Record{}, err
	}
	defer resp.Body.Close()

	var records RecordsResponse
	decoder := json.NewDecoder(resp.Body)
	decoder.Decode(&records)

	for _, rec := range records.Records {
		if rec.Name == fullDomain && rec.Type == "A" {
			return rec, nil
		}
	}
	return Record{}, fmt.Errorf("A-Record nicht gefunden für %s", fullDomain)
}

func updateRecord(zoneID, recordID, name, newIP string) error {
	client := &http.Client{}
	payload := map[string]interface{}{
		"name":    name,
		"type":    "A",
		"value":   newIP,
		"ttl":     config.TTL,
		"zone_id": zoneID,
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
		return fmt.Errorf("Fehler beim Aktualisieren, Status: %s", resp.Status)
	}
	return nil
}

func logAndMail(message string) {
	log.Println(message)
	sendEmail("DNS Update Status", message)
}

func sendEmail(subject, body string) {
	auth := smtp.PlainAuth("", config.SMTP.User, config.SMTP.Password, config.SMTP.Server)
	msg := []byte("To: " + config.SMTP.Recipient + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		body + "\r\n")
	err := smtp.SendMail(config.SMTP.Server+":"+config.SMTP.Port, auth, config.SMTP.User, []string{config.SMTP.Recipient}, msg)
	if err != nil {
		log.Println("Fehler beim Senden der E-Mail:", err)
	}
}
