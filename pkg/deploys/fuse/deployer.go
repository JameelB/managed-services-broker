package fuse

import (
	"net/http"
	"os"

	brokerapi "github.com/aerogear/managed-services-broker/pkg/broker"
	"github.com/aerogear/managed-services-broker/pkg/clients/openshift"
	appsv1 "github.com/openshift/client-go/apps/clientset/versioned/typed/apps/v1"
	k8sClient "github.com/operator-framework/operator-sdk/pkg/k8sclient"
	"github.com/operator-framework/operator-sdk/pkg/util/k8sutil"
	"github.com/pkg/errors"
	glog "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type FuseDeployer struct {
	id string
}

func NewDeployer(id string) *FuseDeployer {
	return &FuseDeployer{id: id}
}

func (fd *FuseDeployer) DoesDeploy(serviceID string) bool {
	return serviceID == "fuse-service-id"
}

func (fd *FuseDeployer) GetCatalogEntries() []*brokerapi.Service {
	glog.Infof("Getting fuse catalog entries")
	return getCatalogServicesObj()
}

func (fd *FuseDeployer) GetID() string {
	return fd.id
}

func (fd *FuseDeployer) Deploy(instanceID, brokerNamespace string, contextProfile brokerapi.ContextProfile, k8sclient kubernetes.Interface, osClientFactory *openshift.ClientFactory) (*brokerapi.CreateServiceInstanceResponse, error) {
	glog.Infof("Deploying fuse from deployer, id: %s", instanceID)

	// Namespace
	namespace := os.Getenv("POD_NAMESPACE")

	// Fuse custom resource
	dashboardURL, err := fd.createFuseCustomResource(namespace, brokerNamespace, contextProfile.Namespace, k8sclient)
	if err != nil {
		glog.Errorln(err)
		return &brokerapi.CreateServiceInstanceResponse{
			Code: http.StatusInternalServerError,
		}, err
	}

	return &brokerapi.CreateServiceInstanceResponse{
		Code:         http.StatusAccepted,
		DashboardURL: dashboardURL,
	}, nil
}

func (fd *FuseDeployer) LastOperation(instanceID string, k8sclient kubernetes.Interface, osclient *openshift.ClientFactory) (*brokerapi.LastOperationResponse, error) {
	glog.Infof("Getting last operation for %s", instanceID)
	namespace := os.Getenv("POD_NAMESPACE")
	podsToWatch := []string{"syndesis-oauthproxy", "syndesis-server", "syndesis-ui"}

	dcClient, err := osclient.AppsClient()
	if err != nil {
		glog.Errorf("failed to create an openshift deployment config client: %+v", err)
		return &brokerapi.LastOperationResponse{
			State:       brokerapi.StateFailed,
			Description: "Failed to create an openshift deployment config client",
		}, errors.Wrap(err, "failed to create an openshift deployment config client")
	}

	for _, v := range podsToWatch {
		state, description, err := fd.getPodStatus(v, namespace, dcClient)
		if state != brokerapi.StateSucceeded {
			return &brokerapi.LastOperationResponse{
				State:       state,
				Description: description,
			}, err
		}
	}

	return &brokerapi.LastOperationResponse{
		State:       brokerapi.StateSucceeded,
		Description: "fuse deployed successfully",
	}, nil
}

func (fd *FuseDeployer) createFuseCustomResource(namespace, brokerNamespace, userNamespace string, k8sclient kubernetes.Interface) (string, error) {
	fuseClient, _, err := k8sClient.GetResourceClient("syndesis.io/v1alpha1", "Syndesis", namespace)
	if err != nil {
		return "", errors.Wrap(err, "failed to create fuse client")
	}

	fuseObj := getFuseObj(userNamespace)

	fuseDashboardURL, err := fd.getRouteHostname(namespace, brokerNamespace, k8sclient)
	if err != nil {
		return "", errors.Wrap(err, "failed to get fuse dashboard url")
	}

	fuseObj.Spec.RouteHostName = fuseDashboardURL
	_, err = fuseClient.Create(k8sutil.UnstructuredFromRuntimeObject(fuseObj))
	if err != nil {
		return "", errors.Wrap(err, "failed to create a fuse custom resource")
	}

	return "https://" + fuseDashboardURL, nil
}

// Get route hostname for fuse
func (fd *FuseDeployer) getRouteHostname(namespace, brokerNamespace string, k8sclient kubernetes.Interface) (string, error) {
	brokerDeployment, err := k8sclient.ExtensionsV1beta1().Deployments(brokerNamespace).Get("msb", metav1.GetOptions{})
	if err != nil {
		glog.Errorf("Failed to get managed services broker deployment: %+v", err)
		return "", errors.Wrap(err, "failed to get managed services broker deployment")
	}

	for _, v := range brokerDeployment.Spec.Template.Spec.Containers[0].Env {
		if v.Name == "ROUTE_SUFFIX" {
			return namespace + "." + v.Value, nil
		}
	}

	glog.Errorf("Failed to get cluster route subdomain from the managed services broker ROUTE_SUFFIX environment variable")
	return "", errors.Wrap(err, "failed to get cluster route subdomain")
}

func (fd *FuseDeployer) getPodStatus(podName, namespace string, dcClient *appsv1.AppsV1Client) (string, string, error) {
	pod, err := dcClient.DeploymentConfigs(namespace).Get(podName, metav1.GetOptions{})
	if err != nil {
		glog.Errorf("Failed to get status of %s: %+v", podName, err)
		return brokerapi.StateFailed,
			"Failed to get status of " + podName,
			errors.Wrap(err, "failed to get status of "+podName)
	}

	for _, v := range pod.Status.Conditions {
		if v.Status == "False" {
			return brokerapi.StateInProgress, v.Message, nil
		}
	}

	return brokerapi.StateSucceeded, "", nil
}
