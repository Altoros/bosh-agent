package monit

import (
	"encoding/xml"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strings"

	"code.google.com/p/go-charset/charset"
	_ "code.google.com/p/go-charset/data" // translations between char sets

	bosherr "github.com/cloudfoundry/bosh-utils/errors"
	boshhttp "github.com/cloudfoundry/bosh-utils/http"
	boshlog "github.com/cloudfoundry/bosh-utils/logger"
)

type httpClient struct {
	startClient     boshhttp.Client
	stopClient      boshhttp.Client
	unmonitorClient boshhttp.Client
	statusClient    boshhttp.Client
	host            string
	username        string
	password        string
	logger          boshlog.Logger
}

// NewHTTPClient creates a new monit client
//
// status & start use the shortClient
// unmonitor & stop use the longClient
func NewHTTPClient(
	host, username, password string,
	shortClient boshhttp.Client,
	longClient boshhttp.Client,
	logger boshlog.Logger,
) Client {
	return httpClient{
		host:            host,
		username:        username,
		password:        password,
		startClient:     shortClient,
		stopClient:      longClient,
		unmonitorClient: longClient,
		statusClient:    shortClient,
		logger:          logger,
	}
}

func (c httpClient) ServicesInGroup(name string) (services []string, err error) {
	status, err := c.status()
	if err != nil {
		return nil, bosherr.WrapError(err, "Getting status from Monit")
	}

	serviceGroup, found := status.ServiceGroups.Get(name)
	if !found {
		return []string{}, nil
	}

	return serviceGroup.Services, nil
}

func (c httpClient) StartService(serviceName string) (err error) {
	response, err := c.makeRequest(c.startClient, c.monitURL(serviceName), "POST", "action=start")
	if err != nil {
		return bosherr.WrapError(err, "Sending start request to monit")
	}

	defer response.Body.Close()

	err = c.validateResponse(response)
	if err != nil {
		return bosherr.WrapErrorf(err, "Starting Monit service %s", serviceName)
	}

	return nil
}

func (c httpClient) StopService(serviceName string) error {
	// TODO: handle err
	c.UnmonitorService(serviceName)

	// TODO: determine whether this should still be the shortClient
	response, err := c.makeRequest(c.stopClient, c.monitURL(serviceName), "POST", "action=stop")
	if err != nil {
		return bosherr.WrapError(err, "Sending stop request to monit")
	}

	defer response.Body.Close()

	err = c.validateResponse(response)
	if err != nil {
		return bosherr.WrapErrorf(err, "Stopping Monit service '%s'", serviceName)
	}

	// TODO: should we break after waiting too long?
	err = c.waitForServiceStop(serviceName)
	if err != nil {
		return bosherr.WrapErrorf(err, "Waiting for Monit service '%s' to stop", serviceName)
	}

	return nil
}

// on stop...

// - when the monit timeout is reached before a successful stop:
//   <status>4096</status>
//   <status_message><![CDATA[failed to stop]]></status_message>
//   Service is stopped.

// - when the monit timeout is reached after a failed stop:
//   <status>4096</status>
//   <status_message><![CDATA[failed to stop]]></status_message>
//   Service remains running.

// - when the stop script exits with a non-0, but the service is stopped:
//   <status>0</status>
//   monit determines that the service did stop

func (c httpClient) waitForServiceStop(serviceName string) error {
	var service *Service

	// TODO: do we want a retry delay?
	for {
		// TODO: log these attempts
		// TODO: handle the error
		service, _ = c.getServiceByName(serviceName)
		if service == nil {
			return bosherr.Errorf("Service '%s' was not found", serviceName)
		}

		if !service.Pending {
			// All queued actions finished executing
			break
		}
	}

	// TODO: test this
	if service.Errored {
		return bosherr.Errorf("Service '%s' errored with message: '%s'", serviceName, service.StatusMessage)
	}

	return nil
}

func (c httpClient) getServiceByName(serviceName string) (service *Service, err error) {
	st, err := c.Status()
	if err != nil {
		return nil, bosherr.WrapError(err, "Sending status request to monit")
	}

	services := st.ServicesInGroup("vcap")

	for _, service := range services {
		if service.Name == serviceName {
			return &service, nil
		}
	}

	return nil, nil
}

func (c httpClient) UnmonitorService(serviceName string) error {
	response, err := c.makeRequest(c.unmonitorClient, c.monitURL(serviceName), "POST", "action=unmonitor")
	if err != nil {
		return bosherr.WrapError(err, "Sending unmonitor request to monit")
	}

	defer response.Body.Close()

	err = c.validateResponse(response)
	if err != nil {
		return bosherr.WrapErrorf(err, "Unmonitoring Monit service %s", serviceName)
	}

	return nil
}

func (c httpClient) Status() (Status, error) {
	return c.status()
}

func (c httpClient) status() (status, error) {
	c.logger.Debug("http-client", "status function called")
	url := c.monitURL("_status2")
	url.RawQuery = "format=xml"

	response, err := c.makeRequest(c.statusClient, url, "GET", "")
	if err != nil {
		return status{}, bosherr.WrapError(err, "Sending status request to monit")
	}

	defer response.Body.Close()

	err = c.validateResponse(response)
	if err != nil {
		return status{}, bosherr.WrapError(err, "Getting monit status")
	}

	decoder := xml.NewDecoder(response.Body)
	decoder.CharsetReader = charset.NewReader

	var st status

	err = decoder.Decode(&st)
	if err != nil {
		return status{}, bosherr.WrapError(err, "Unmarshalling Monit status")
	}

	return st, nil
}

func (c httpClient) monitURL(thing string) url.URL {
	return url.URL{
		Scheme: "http",
		Host:   c.host,
		Path:   path.Join("/", thing),
	}
}

func (c httpClient) validateResponse(response *http.Response) error {
	if response.StatusCode == http.StatusOK {
		return nil
	}

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return bosherr.WrapError(err, "Reading body of failed Monit response")
	}

	c.logger.Debug("http-client", "Request failed with %s: %s", response.Status, string(body))

	return bosherr.Errorf("Request failed with %s: %s", response.Status, string(body))
}

func (c httpClient) makeRequest(client boshhttp.Client, target url.URL, method, requestBody string) (*http.Response, error) {
	c.logger.Debug("http-client", "Monit request: url='%s' body='%s'", target.String(), requestBody)

	request, err := http.NewRequest(method, target.String(), strings.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	request.SetBasicAuth(c.username, c.password)

	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return client.Do(request)
}
