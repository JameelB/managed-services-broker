/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"fmt"
	"sync"

	"github.com/pkg/errors"

	"net/http"

	"github.com/aerogear/managed-services-broker/pkg/broker"
	brokerapi "github.com/aerogear/managed-services-broker/pkg/broker"
	glog "github.com/sirupsen/logrus"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

//Deployer deploys a service from this broker
type Deployer interface {
	GetCatalogEntries() []*brokerapi.Service
	Deploy(id string, k8sclient kubernetes.Interface, config *rest.Config) (*brokerapi.CreateServiceInstanceResponse, error)
	GetID() string
	DoesDeploy(serviceID string) bool
}

// Controller defines the APIs that all controllers are expected to support. Implementations
// should be concurrency-safe
type Controller interface {
	Catalog() (*broker.Catalog, error)

	GetServiceInstanceLastOperation(instanceID, serviceID, planID, operation string) (*broker.LastOperationResponse, error)
	CreateServiceInstance(instanceID string, req *broker.CreateServiceInstanceRequest) (*broker.CreateServiceInstanceResponse, error)
	RemoveServiceInstance(instanceID, serviceID, planID string, acceptsIncomplete bool) (*broker.DeleteServiceInstanceResponse, error)

	Bind(instanceID, bindingID string, req *broker.BindingRequest) (*broker.CreateServiceBindingResponse, error)
	UnBind(instanceID, bindingID, serviceID, planID string) error

	RegisterDeployer(deployer Deployer)
}

type errNoSuchInstance struct {
	instanceID string
}

func (e errNoSuchInstance) Error() string {
	return fmt.Sprintf("no such instance with ID %s", e.instanceID)
}

type userProvidedServiceInstance struct {
	Name       string
	Credential *brokerapi.Credential
}

type userProvidedController struct {
	rwMutex             sync.RWMutex
	instanceMap         map[string]*userProvidedServiceInstance
	k8sclient           kubernetes.Interface
	brokerNS            string
	kubeconfig          *rest.Config
	registeredDeployers map[string]Deployer
}

// CreateController creates an instance of a User Provided service broker controller.
func CreateController(brokerNS string, k8sclient kubernetes.Interface, kubeconfig *rest.Config) Controller {
	var instanceMap = make(map[string]*userProvidedServiceInstance)
	return &userProvidedController{
		instanceMap:         instanceMap,
		brokerNS:            brokerNS,
		k8sclient:           k8sclient,
		kubeconfig:          kubeconfig,
		registeredDeployers: map[string]Deployer{},
	}
}

var services []*brokerapi.Service

func (c *userProvidedController) RegisterDeployer(deployer Deployer) {
	c.registeredDeployers[deployer.GetID()] = deployer
}

func (c *userProvidedController) Catalog() (*brokerapi.Catalog, error) {
	glog.Info("Catalog()")
	services = []*brokerapi.Service{}
	for _, deployer := range c.registeredDeployers {
		services = append(services, deployer.GetCatalogEntries()...)
	}

	return &brokerapi.Catalog{
		Services: services,
	}, nil
}

func (c *userProvidedController) CreateServiceInstance(
	id string,
	req *brokerapi.CreateServiceInstanceRequest,
) (*brokerapi.CreateServiceInstanceResponse, error) {
	for _, deployer := range c.registeredDeployers {
		if deployer.DoesDeploy(req.ServiceID) {
			return deployer.Deploy(id, c.k8sclient, c.kubeconfig)
		}
	}

	return &brokerapi.CreateServiceInstanceResponse{Code: http.StatusInternalServerError, Operation: "provision"}, nil
}

func (c *userProvidedController) GetServiceInstanceLastOperation(
	instanceID,
	serviceID,
	planID,
	operation string,
) (*brokerapi.LastOperationResponse, error) {
	glog.Info("GetServiceInstanceLastOperation()", "operation "+operation, serviceID)

	return nil, errors.New("Unimplemented")
}

func (c *userProvidedController) RemoveServiceInstance(
	instanceID,
	serviceID,
	planID string,
	acceptsIncomplete bool,
) (*brokerapi.DeleteServiceInstanceResponse, error) {
	glog.Info("RemoveServiceInstance()", instanceID)
	return &brokerapi.DeleteServiceInstanceResponse{}, nil
}

func (c *userProvidedController) Bind(
	instanceID,
	bindingID string,
	req *brokerapi.BindingRequest,
) (*brokerapi.CreateServiceBindingResponse, error) {
	glog.Info("Bind()")
	c.rwMutex.RLock()
	defer c.rwMutex.RUnlock()
	instance, ok := c.instanceMap[instanceID]
	if !ok {
		return nil, errNoSuchInstance{instanceID: instanceID}
	}
	cred := instance.Credential
	return &brokerapi.CreateServiceBindingResponse{Credentials: *cred}, nil
}

func (c *userProvidedController) UnBind(instanceID, bindingID, serviceID, planID string) error {
	glog.Info("UnBind()")
	// Since we don't persist the binding, there's nothing to do here.
	return nil
}
