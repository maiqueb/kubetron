// TODO: remove orhpan interfaces in another thread
// TODO: Smarter handling of nicPoolSize
// TODO: use prestart container request, no need to wait
// TODO: cleanup if a step fails
// TODO: load networkSpec annotation name from common module
package deviceplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os/exec"
	"strconv"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	dockercli "github.com/docker/docker/client"
	"github.com/golang/glog"
	"github.com/phoracek/kubetron/pkg/spec"
	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	pluginapi "k8s.io/kubernetes/pkg/kubelet/apis/deviceplugin/v1beta1"
)

const (
	// devicesPoolSize represents amount of available "devices", i.e. overlay attachments, where one pod, no matter how many networks it requests, takes one overlay attachment
	devicesPoolSize = 1000
	// devicepluginCheckpointPath keep absolute path of DP checkpoint file, this file contains DP related information, such as which Pod (UID) has assigned which device ID
	devicepluginCheckpointPath = "/var/lib/kubelet/device-plugins/kubelet_internal_checkpoint"
	// newtworksSpecAnnotationName is a name of Pod's annotation that keeps detailed information about requested network attachments, it is generated by Admission
	networksSpecAnnotationName = "kubetron.network.kubevirt.io/networksSpec"
)

// DevicePlugin implements deviceplugin v1beta1 interface. It reports available (fake) devices and connects Pod to requested networks
type DevicePlugin struct{}

// GetDevicePluginOptions is used to pass parameters to device manager
func (dp DevicePlugin) GetDevicePluginOptions(ctx context.Context, in *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	return &pluginapi.DevicePluginOptions{
		PreStartRequired: false,
	}, nil
}

// ListAndWatch returns list of devices available on the host. Since we don't work with limited physical interfaces, we report set of virtual devices that are later used to identify Pod
func (dp DevicePlugin) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	glog.V(4).Infof("Starting list and watch")

	for {
		var devs []*pluginapi.Device
		for i := 0; i < devicesPoolSize; i++ {
			devs = append(devs, &pluginapi.Device{
				ID:     fmt.Sprintf("dev-%02d", i),
				Health: pluginapi.Healthy,
			})
		}
		s.Send(&pluginapi.ListAndWatchResponse{Devices: devs})
		time.Sleep(10 * time.Second)
	}
	return nil
}

// Allocate is executed when a new Pod appears and it requires requested resource to be allocated and attached. In our case we create new interface on OVS integration bridge (mapped to selected OVN network) and pass it to the Pod
// TODO: cleanup if fails
func (dp DevicePlugin) Allocate(ctx context.Context, r *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	responses := pluginapi.AllocateResponse{}

	// TODO: is this needed?
	for _, _ = range r.ContainerRequests {
		response := pluginapi.ContainerAllocateResponse{}
		responses.ContainerResponses = append(responses.ContainerResponses, &response)
	}

	// Validate that exactly one device was requested, we are not able to handle different situation
	if len(r.ContainerRequests) != 1 {
		return nil, fmt.Errorf("Allocate request must contain exactly one container request")
	}
	if len(r.ContainerRequests[0].DevicesIDs) != 1 {
		return nil, fmt.Errorf("Allocate request must contain exactly one device")
	}

	allocatedDeviceID := r.ContainerRequests[0].DevicesIDs[0]

	go func() {
		// Wait a bit to make sure that allocated device ID will appear in checkpoint file
		time.Sleep(10 * time.Second)

		// Lookup Pod UID based on allocated device ID
		podUID, err := findPodUID(allocatedDeviceID)
		if err != nil {
			glog.Errorf("Failed to find pod UID: %v", err)
			return
		}

		// Obtain Pod specification from Kubernetes
		pod, err := findPod(podUID)
		if err != nil {
			glog.Errorf("Failed to find pod with given PodUID: %v", err)
			return
		}

		// Parse Pod specification and obtain networksSpec details
		networksSpec, err := buildNetworksSpec(pod)
		if err != nil {
			glog.Errorf("Failed to read networks spec: %v", err)
			return
		}

		// Containers made by Kubernetes are usually in this format: k8s_POD_$PODNAME_$PODNAMESPACE_$RANDOMSUFFIX
		containerName := fmt.Sprintf("k8s_POD_%s_%s", pod.Name, pod.Namespace)

		// Find container PID based on its name, it will be later used to access its network namespace
		containerPid, err := findContainerPid(containerName)
		if err != nil {
			glog.Errorf("Failed to find container PID: %v", err)
			return
		}

		// Iterate all network requests (assigned ports), create OVS interface for each, do needed configuration and pass it to Pod network namespace
		// TODO: run in parallel, make sure to precreate netns (colission)
		for _, spec := range *networksSpec {
			if err := exec.Command("attach-pod", containerName, spec.PortName, spec.PortID, spec.MacAddress, strconv.Itoa(containerPid)).Run(); err != nil {
				// TODO: include logs here
				glog.Errorf("attach-pod failed, please check logs in Daemon Set /var/log/attach-pod.err.log")
			}
		}
	}()

	return &responses, nil
}

// findPodUID uses deviceplugin checkpoint file hack to find Pod UID based on name of allocated device
func findPodUID(deviceID string) (string, error) {
	// Read checkpoint file
	checkpointRaw, err := ioutil.ReadFile(devicepluginCheckpointPath)
	if err != nil {
		return "", fmt.Errorf("failed to read device plugin checkpoint file: %v", err)
	}

	// Try to parse the checkpoint file
	var checkpoint map[string]interface{}
	err = json.Unmarshal(checkpointRaw, &checkpoint)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal device plugin checkpoint file: %v", err)
	}

	// Iterate pod-devices assignments, try to find a Pod that has allocated device in its list
	for _, entry := range checkpoint["PodDeviceEntries"].([]interface{}) {
		if entry.(map[string]interface{})["ResourceName"].(string) != "kubetron.network.kubevirt.io/main" { // TODO, get it from ns and reserved name
			continue
		}
		for _, foundDeviceID := range entry.(map[string]interface{})["DeviceIDs"].([]interface{}) {
			if foundDeviceID.(string) == deviceID {
				podUID := entry.(map[string]interface{})["PodUID"].(string)
				return podUID, nil
			}
		}
	}

	return "", fmt.Errorf("failed to find a pod with matching device ID")
}

// findPod is a helper that returns Pod object from Kubernernetes based on its UID
func findPod(podUID string) (*v1.Pod, error) {
	// TODO: keep client in DP struct
	kubeClientConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to obtain kubernetes client config: %v", err)
	}
	kubeclient, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to intialize kubernetes client: %v", err)
	}

	// List all pods
	// TODO: can we filter it just to annotations or access Pod directly using UID?
	pods, err := kubeclient.CoreV1().Pods("").List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	// Try to find Pod with passed UID
	for _, pod := range pods.Items {
		if string(pod.UID) == podUID {
			return &pod, nil
		}
	}

	return nil, fmt.Errorf("failed to find a pod with matching ID")
}

// buildNetworksSpec reads Pod's annotation and tries to parse its networksSpec data
func buildNetworksSpec(pod *v1.Pod) (*spec.NetworksSpec, error) {
	var networksSpec *spec.NetworksSpec

	annotations := pod.ObjectMeta.GetAnnotations()
	networksSpecAnnotation, _ := annotations[networksSpecAnnotationName]

	err := json.Unmarshal([]byte(networksSpecAnnotation), &networksSpec)

	return networksSpec, err
}

// findContainerPid queries Docker for its containers and tries to find the one with passed name, it returns container's PID that is later used to access its network namespace
func findContainerPid(containerName string) (int, error) {
	// TODO: keep client in DP struct
	dockerclient, err := dockercli.NewEnvClient()
	if err != nil {
		return 0, fmt.Errorf("failed to intialize docker client: %v", err)
	}

	// Retry in case the contianer is still being created
	for i := 0; i <= 10; i++ {
		// List all Docker containers
		containers, err := dockerclient.ContainerList(context.Background(), dockertypes.ContainerListOptions{})
		if err != nil {
			return 0, fmt.Errorf("failed to list docker containers: %v", err)
		}

		// Iterate found containers
		for _, container := range containers {
			config, err := dockerclient.ContainerInspect(context.Background(), container.ID)
			if err != nil {
				return 0, fmt.Errorf("failed to inspect docker container: %v", err)
			}

			if strings.Contains(config.Name, containerName) {
				return config.State.Pid, nil
			}
		}

		glog.V(4).Infof("Did not find container %s during the %d. try, retrying in 10 seconds", containerName, i+1)
		time.Sleep(10 * time.Second)
	}

	return 0, fmt.Errorf("failed to find container PID")

}

// PreStartContainer is currently unused method that should be later used to move OVS interface to Pod and configure it
// TODO: use this instead of separate thread during Allocate
func (dp DevicePlugin) PreStartContainer(ctx context.Context, r *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	var response pluginapi.PreStartContainerResponse
	return &response, nil
}
