package climax

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type ClimaxConfig struct {
	BaseUrl  string `yaml:"base_url" env:"climax_baseurl" json:"climax_baseurl"`
	Username string `yaml:"username" env:"climax_username" json:"climax_username"`
	Password string `yaml:"password" env:"climax_password" json:"climax_password"`
}

func (cfg *ClimaxConfig) AddHeaders(header *http.Header) {
	header.Add("Authorization", "Basic YWRtaW46YWRtaW4xMjM0")
	header.Add("Accept", "application/json")
}

type ApiResponse struct {
	Result  int    `json:"result"`
	Message string `json:"message"`
}

func (cfg *ClimaxConfig) SetDeviceSwitch(deviceId string, switchState bool, pd ...string) error {
	urlStr := cfg.BaseUrl + "/action/deviceSwitchPSSPost"

	var pdValue string
	if len(pd) > 0 {
		pdValue = pd[0]
	} else {
		pdValue = ""
	}

	switchValue := "0"
	if switchState {
		switchValue = "1"
	}

	formData := url.Values{
		"id":     {deviceId},
		"switch": {switchValue},
		"pd":     {pdValue},
	}
	log.Printf("setDeviceSwitch: formData: %s\n", formData.Encode())

	req, err := http.NewRequest("POST", urlStr, strings.NewReader(formData.Encode()))
	if err != nil {
		log.Printf("setDeviceSwitch: could not create request: %s\n", err)
		return err
	}

	cfg.AddHeaders(&req.Header)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("setDeviceSwitch: error making http request: %s\n", err)
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Printf("setDeviceSwitch: could not read response body: %s\n", err)
		return err
	}

	var apiResp ApiResponse
	err = json.Unmarshal(body, &apiResp)
	if err != nil {
		log.Printf("setDeviceSwitch: could not unmarshal JSON: %s\n %s", err, string(body))
		return err
	}

	if apiResp.Result != 1 {
		errMsg := fmt.Sprintf("setDeviceSwitch: failed with message: %s", apiResp.Message)
		log.Println(errMsg)
		return errors.New(errMsg)
	}

	log.Printf("setDeviceSwitch: successful - %s", apiResp.Message)
	return nil
}

func (cfg *ClimaxConfig) GetDevices() ([]DeviceInterface, error) {

	url := cfg.BaseUrl + "/action/deviceListGet"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("getDevices: could not create request: %s\n", err)
		return nil, err
	}
	cfg.AddHeaders(&req.Header)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("getDevices: error making http request: %s\n", err)
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Printf("getDevices: could not read response body: %s\n", err)
		return nil, err
	}

	var result struct {
		Senrows []map[string]interface{} `json:"senrows"`
	}
	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Printf("getDevices: could not Unmarshal json: %s\n %s", err, string(body))
		return nil, err
	}

	var devices []DeviceInterface
	for _, rd := range result.Senrows {
		deviceType, ok := rd["type"].(float64)
		if !ok {
			log.Printf("getDevices: error determining device type\n")
			continue
		}

		bytes, _ := json.Marshal(rd)
		switch DeviceType(deviceType) {
		case Temperature_Sensor:
			var tempSensor TemperatureSensor
			json.Unmarshal(bytes, &tempSensor)
			tempSensor.Temperature = parseTemperature(tempSensor.Status)
			devices = append(devices, tempSensor)
		case Power_Switch, Power_Switch_Meter:
			var powerSwitch PowerSwitchMeter
			json.Unmarshal(bytes, &powerSwitch)
			powerSwitch.OnOff, powerSwitch.Power, powerSwitch.Energy = parsePowerSwitchMeterStatus(powerSwitch.Status)
			devices = append(devices, powerSwitch)
		case Smoke_Detector, Hue_Sensor:
			var device Device
			json.Unmarshal(bytes, &device)
			devices = append(devices, device)

		default:
			log.Printf("getDevices: error determining device type %f\n", deviceType)
			continue
		}
	}

	return devices, nil
}

func (cfg *ClimaxConfig) GetDeviceHistory(count_optional ...int) ([]DeviceHistory, error) {
	urlStr := cfg.BaseUrl + "/action/historyGet"

	count := 10
	if len(count_optional) == 1 {
		count = count_optional[0]
	}
	formData := url.Values{
		"max_count": {strconv.Itoa(count)},
	}

	req, err := http.NewRequest("POST", urlStr, strings.NewReader(formData.Encode()))
	if err != nil {
		log.Printf("getDevices: could not create request: %s\n", err)
		return nil, err
	}
	cfg.AddHeaders(&req.Header)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("getDevices: error making http request: %s\n", err)
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Printf("getDevices: could not read response body: %s\n", err)
		return nil, err
	}

	type getDeviceHistoryResult struct {
		Hisrows []DeviceHistory
	}

	var data getDeviceHistoryResult
	err = json.Unmarshal(body, &data)
	if err != nil {
		log.Printf("getDevices: could not Unmarshal json: %s\n %s", err, string(body))
		return nil, err
	}

	return data.Hisrows, nil
}
