// Copyright (c) 2019 Sorint.lab S.p.A.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ercole-io/ercole-agent-exadata/config"
	"github.com/ercole-io/ercole-agent-exadata/marshal"
	"github.com/ercole-io/ercole-agent-exadata/model"
	"github.com/ercole-io/ercole-agent-exadata/scheduler"
	"github.com/ercole-io/ercole-agent-exadata/scheduler/storage"
)

//var configuration config.Configuration
var version = "latest"
var hostDataSchemaVersion = 4

func main() {

	rand.Seed(243243)
	configuration := config.ReadConfig()

	buildData(configuration) // first run
	memStorage := storage.NewMemoryStorage()
	scheduler := scheduler.New(memStorage)

	_, err := scheduler.RunEvery(time.Duration(configuration.Frequency)*time.Hour, buildData, configuration)

	if err != nil {
		log.Fatal("Error sending data", err)
	}

	scheduler.Start()
	scheduler.Wait()

}

func buildData(configuration config.Configuration) {

	out := fetcher("host")
	host := marshal.Host(out)

	host.Environment = configuration.Envtype
	host.Location = configuration.Location
	out = fetcher("filesystem")
	filesystems := marshal.Filesystems(out)

	out = fetcher("exadata-info")
	exadataDevices := marshal.ExadataDevices(out)
	out = fetcher("exadata-storage-status")
	exadataCellDisks := marshal.ExadataCellDisks(out)

	//Join exadataDevices with exadataCellDisks
	for _, cd := range exadataCellDisks {
		for i := range exadataDevices {
			if cd.StorageServerName == exadataDevices[i].Hostname {
				if exadataDevices[i].CellDisks == nil {
					exadataDevices[i].CellDisks = []model.ExadataCellDisk{}
				}
				exadataDevices[i].CellDisks = append(exadataDevices[i].CellDisks, cd)
			}
		}
	}

	hostData := new(model.HostData)
	extraInfo := new(model.ExtraInfo)
	extraInfo.Filesystems = filesystems
	extraInfo.Databases = []model.Database{}
	extraInfo.Exadata.Devices = exadataDevices
	hostData.Extra = *extraInfo
	hostData.Info = host
	hostData.Hostname = host.Hostname
	// override host name with the one in config if != default
	if configuration.Hostname != "default" {
		hostData.Hostname = configuration.Hostname
	}
	hostData.Environment = configuration.Envtype
	hostData.Location = configuration.Location
	hostData.HostType = configuration.HostType
	hostData.Version = version
	hostData.HostDataSchemaVersion = hostDataSchemaVersion
	hostData.Databases = ""
	hostData.Schemas = ""

	sendData(hostData, configuration)
}

func sendData(data *model.HostData, configuration config.Configuration) {
	log.Println("Sending data...")

	b, _ := json.Marshal(data)
	s := string(b)

	log.Println("Data:", s)

	hostType := configuration.HostType
	if hostType == "" {
		hostType = "non-defined"
	}

	client := &http.Client{}

	//Disable certificate validation if enableServerValidation is false
	if configuration.EnableServerValidation == false {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	url, err := url.Parse(configuration.Serverurl)
	if err != nil {
		log.Fatal(err)
	} else {
		var ok bool
		_, ok = url.Query()["HostType"]
		if !ok {
			query := url.Query()
			query.Add("HostType", hostType)
			url.RawQuery = query.Encode()
		}
	}

	req, err := http.NewRequest("POST", url.String(), bytes.NewReader(b))

	req.Header.Add("Content-Type", "application/json")
	// auth := configuration.Serverusr + ":" + configuration.Serverpsw
	// authEnc := b64.StdEncoding.EncodeToString([]byte(auth))
	// req.Header.Add("Authorization", "Basic "+authEnc)
	req.SetBasicAuth(configuration.Serverusr, configuration.Serverpsw)
	resp, err := client.Do(req)

	sendResult := "FAILED"

	if err != nil {
		log.Println("Error sending data", err)
	} else {
		log.Println("Response status:", resp.Status)
		if resp.StatusCode == 200 {
			sendResult = "SUCCESS"
		}
		defer resp.Body.Close()
	}

	log.Println("Sending result:", sendResult)

}

func fetcher(fetcherName string, args ...string) []byte {
	var (
		cmd    *exec.Cmd
		err    error
		stdout bytes.Buffer
		stderr bytes.Buffer
	)

	log.Println("Fetching " + fetcherName + ": " + strings.Join(args, " "))

	baseDir := getBaseDir()

	cmd = exec.Command(baseDir+"/fetch/"+fetcherName, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	// log.Println(stderr)
	if len(stderr.Bytes()) > 0 {
		log.Print(string(stderr.Bytes()))
	}

	if err != nil {
		log.Fatal(err)
	}

	return stdout.Bytes()
}

func getBaseDir() string {

	s, _ := os.Readlink("/proc/self/exe")

	s = filepath.Dir(s)

	return s
}
