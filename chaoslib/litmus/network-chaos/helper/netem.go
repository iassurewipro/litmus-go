package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	clients "github.com/litmuschaos/litmus-go/pkg/clients"
	"github.com/litmuschaos/litmus-go/pkg/events"
	experimentEnv "github.com/litmuschaos/litmus-go/pkg/generic/network-chaos/environment"
	experimentTypes "github.com/litmuschaos/litmus-go/pkg/generic/network-chaos/types"
	"github.com/litmuschaos/litmus-go/pkg/log"
	"github.com/litmuschaos/litmus-go/pkg/result"
	"github.com/litmuschaos/litmus-go/pkg/types"
	"github.com/litmuschaos/litmus-go/pkg/utils/common"
	"github.com/pkg/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientTypes "k8s.io/apimachinery/pkg/types"
)

const (
	qdiscNotFound    = "Cannot delete qdisc with handle of zero"
	qdiscNoFileFound = "RTNETLINK answers: No such file or directory"
)

var err error
var inject, abort chan os.Signal

func main() {

	experimentsDetails := experimentTypes.ExperimentDetails{}
	clients := clients.ClientSets{}
	eventsDetails := types.EventDetails{}
	chaosDetails := types.ChaosDetails{}
	resultDetails := types.ResultDetails{}

	// inject channel is used to transmit signal notifications.
	inject = make(chan os.Signal, 1)
	// Catch and relay certain signal(s) to inject channel.
	signal.Notify(inject, os.Interrupt, syscall.SIGTERM)

	// abort channel is used to transmit signal notifications.
	abort = make(chan os.Signal, 1)
	// Catch and relay certain signal(s) to abort channel.
	signal.Notify(abort, os.Interrupt, syscall.SIGTERM)

	//Getting kubeConfig and Generate ClientSets
	if err := clients.GenerateClientSetFromKubeConfig(); err != nil {
		log.Fatalf("Unable to Get the kubeconfig, err: %v", err)
	}

	//Fetching all the ENV passed for the helper pod
	log.Info("[PreReq]: Getting the ENV variables")
	GetENV(&experimentsDetails)

	// Intialise the chaos attributes
	experimentEnv.InitialiseChaosVariables(&chaosDetails, &experimentsDetails)

	// Intialise Chaos Result Parameters
	types.SetResultAttributes(&resultDetails, chaosDetails)

	// Set the chaos result uid
	result.SetResultUID(&resultDetails, clients, &chaosDetails)

	err := PreparePodNetworkChaos(&experimentsDetails, clients, &eventsDetails, &chaosDetails, &resultDetails)
	if err != nil {
		log.Fatalf("helper pod failed, err: %v", err)
	}

}

//PreparePodNetworkChaos contains the prepration steps before chaos injection
func PreparePodNetworkChaos(experimentsDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets, eventsDetails *types.EventDetails, chaosDetails *types.ChaosDetails, resultDetails *types.ResultDetails) error {

	containerID, err := GetContainerID(experimentsDetails, clients)
	if err != nil {
		return err
	}
	// extract out the pid of the target container
	targetPID, err := GetPID(experimentsDetails, containerID)
	if err != nil {
		return err
	}

	// record the event inside chaosengine
	if experimentsDetails.EngineName != "" {
		msg := "Injecting " + experimentsDetails.ExperimentName + " chaos on application pod"
		types.SetEngineEventAttributes(eventsDetails, types.ChaosInject, msg, "Normal", chaosDetails)
		events.GenerateEvents(eventsDetails, clients, chaosDetails, "ChaosEngine")
	}

	// watching for the abort signal and revert the chaos
	go abortWatcher(targetPID)

	// injecting network chaos inside target container
	if err = InjectChaos(experimentsDetails, targetPID); err != nil {
		return err
	}

	log.Infof("[Chaos]: Waiting for %vs", experimentsDetails.ChaosDuration)

	common.WaitForDuration(experimentsDetails.ChaosDuration)

	log.Info("[Chaos]: Stopping the experiment")

	// cleaning the netem process after chaos injection
	if err = Killnetem(targetPID); err != nil {
		return err
	}

	return nil
}

//GetContainerID extract out the container id of the target container
func GetContainerID(experimentDetails *experimentTypes.ExperimentDetails, clients clients.ClientSets) (string, error) {

	var containerID string
	switch experimentDetails.ContainerRuntime {
	case "docker":
		host := "unix://" + experimentDetails.SocketPath
		// deriving the container id of the pause container
		cmd := "sudo docker --host " + host + " ps | grep k8s_POD_" + experimentDetails.TargetPods + "_" + experimentDetails.AppNS + " | awk '{print $1}'"
		out, err := exec.Command("/bin/sh", "-c", cmd).CombinedOutput()
		if err != nil {
			log.Error(fmt.Sprintf("[docker]: Failed to run docker ps command: %s", string(out)))
			return "", err
		}
		containerID = strings.TrimSpace(string(out))
	case "containerd", "crio":
		pod, err := clients.KubeClient.CoreV1().Pods(experimentDetails.AppNS).Get(experimentDetails.TargetPods, v1.GetOptions{})
		if err != nil {
			return "", err
		}
		// filtering out the container id from the details of containers inside containerStatuses of the given pod
		// container id is present in the form of <runtime>://<container-id>
		for _, container := range pod.Status.ContainerStatuses {
			if container.Name == experimentDetails.TargetContainer {
				containerID = strings.Split(container.ContainerID, "//")[1]
				break
			}
		}
	default:
		return "", errors.Errorf("%v container runtime not suported", experimentDetails.ContainerRuntime)
	}
	log.Infof("containerid: %v", containerID)

	return containerID, nil
}

//GetPID extract out the PID of the target container
func GetPID(experimentDetails *experimentTypes.ExperimentDetails, containerID string) (int, error) {
	var PID int

	switch experimentDetails.ContainerRuntime {
	case "docker":
		host := "unix://" + experimentDetails.SocketPath
		// deriving pid from the inspect out of target container
		out, err := exec.Command("sudo", "docker", "--host", host, "inspect", containerID).CombinedOutput()
		if err != nil {
			log.Error(fmt.Sprintf("[docker]: Failed to run docker inspect: %s", string(out)))
			return 0, err
		}
		// parsing data from the json output of inspect command
		PID, err = parsePIDFromJSON(out, experimentDetails.ContainerRuntime)
		if err != nil {
			log.Error(fmt.Sprintf("[docker]: Failed to parse json from docker inspect output: %s", string(out)))
			return 0, err
		}
	case "containerd", "crio":
		// deriving pid from the inspect out of target container
		endpoint := "unix://" + experimentDetails.SocketPath
		out, err := exec.Command("sudo", "crictl", "-i", endpoint, "-r", endpoint, "inspect", containerID).CombinedOutput()
		if err != nil {
			log.Error(fmt.Sprintf("[cri]: Failed to run crictl: %s", string(out)))
			return 0, err
		}
		// parsing data from the json output of inspect command
		PID, err = parsePIDFromJSON(out, experimentDetails.ContainerRuntime)
		if err != nil {
			log.Errorf(fmt.Sprintf("[cri]: Failed to parse json from crictl output: %s", string(out)))
			return 0, err
		}
	default:
		return 0, errors.Errorf("%v container runtime not suported", experimentDetails.ContainerRuntime)
	}

	log.Info(fmt.Sprintf("[cri]: Container ID=%s has process PID=%d", containerID, PID))

	return PID, nil
}

// CrictlInspectResponse JSON representation of crictl inspect command output
// in crio, pid is present inside pid attribute of inspect output
// in containerd, pid is present inside `info.pid` of inspect output
type CrictlInspectResponse struct {
	Info InfoDetails `json:"info"`
}

// InfoDetails JSON representation of crictl inspect command output
type InfoDetails struct {
	RuntimeSpec RuntimeDetails `json:"runtimeSpec"`
	PID         int            `json:"pid"`
}

// RuntimeDetails contains runtime details
type RuntimeDetails struct {
	Linux LinuxAttributes `json:"linux"`
}

// LinuxAttributes contains all the linux attributes
type LinuxAttributes struct {
	Namespaces []Namespace `json:"namespaces"`
}

// Namespace contains linux namespace details
type Namespace struct {
	Type string `json:"type"`
	Path string `json:"path"`
}

// DockerInspectResponse JSON representation of docker inspect command output
type DockerInspectResponse struct {
	State StateDetails `json:"state"`
}

// StateDetails JSON representation of docker inspect command output
type StateDetails struct {
	PID int `json:"pid"`
}

//parsePIDFromJSON extract the pid from the json output
func parsePIDFromJSON(j []byte, runtime string) (int, error) {
	var pid int
	// namespaces are present inside `info.runtimeSpec.linux.namespaces` of inspect output
	// linux namespace of type network contains pid, in the form of `/proc/<pid>/ns/net`
	switch runtime {
	case "docker":
		// in docker, pid is present inside state.pid attribute of inspect output
		var resp []DockerInspectResponse
		if err := json.Unmarshal(j, &resp); err != nil {
			return 0, err
		}
		pid = resp[0].State.PID
	case "containerd":
		var resp CrictlInspectResponse
		if err := json.Unmarshal(j, &resp); err != nil {
			return 0, err
		}
		for _, namespace := range resp.Info.RuntimeSpec.Linux.Namespaces {
			if namespace.Type == "network" {
				value := strings.Split(namespace.Path, "/")[2]
				pid, _ = strconv.Atoi(value)
			}
		}
	case "crio":
		var info InfoDetails
		if err := json.Unmarshal(j, &info); err != nil {
			return 0, err
		}
		pid = info.PID
		if pid == 0 {
			var resp CrictlInspectResponse
			if err := json.Unmarshal(j, &resp); err != nil {
				return 0, err
			}
			pid = resp.Info.PID
		}
	default:
		return 0, errors.Errorf("[cri]: No supported container runtime, runtime: %v", runtime)
	}
	if pid == 0 {
		return 0, errors.Errorf("[cri]: No running target container found, pid: %d", pid)
	}

	return pid, nil
}

// InjectChaos inject the network chaos in target container
// it is using nsenter command to enter into network namespace of target container
// and execute the netem command inside it.
func InjectChaos(experimentDetails *experimentTypes.ExperimentDetails, pid int) error {

	netemCommands := os.Getenv("NETEM_COMMAND")
	destinationIPs := os.Getenv("DESTINATION_IPS")

	select {
	case <-inject:
		// stopping the chaos execution, if abort signal recieved
		os.Exit(1)
	default:
		if destinationIPs == "" {
			tc := fmt.Sprintf("sudo nsenter -t %d -n tc qdisc replace dev %s root netem %v", pid, experimentDetails.NetworkInterface, netemCommands)
			cmd := exec.Command("/bin/bash", "-c", tc)
			out, err := cmd.CombinedOutput()
			log.Info(cmd.String())
			if err != nil {
				log.Error(string(out))
				return err
			}
		} else {

			ips := strings.Split(destinationIPs, ",")
			var uniqueIps []string

			// removing duplicates ips from the list, if any
			for i := range ips {
				isPresent := false
				for j := range uniqueIps {
					if ips[i] == uniqueIps[j] {
						isPresent = true
					}
				}
				if !isPresent {
					uniqueIps = append(uniqueIps, ips[i])
				}

			}

			// Create a priority-based queue
			// This instantly creates classes 1:1, 1:2, 1:3
			priority := fmt.Sprintf("sudo nsenter -t %v -n tc qdisc replace dev %v root handle 1: prio", pid, experimentDetails.NetworkInterface)
			cmd := exec.Command("/bin/bash", "-c", priority)
			out, err := cmd.CombinedOutput()
			log.Info(cmd.String())
			if err != nil {
				log.Error(string(out))
				return err
			}

			// Add queueing discipline for 1:3 class.
			// No traffic is going through 1:3 yet
			traffic := fmt.Sprintf("sudo nsenter -t %v -n tc qdisc replace dev %v parent 1:3 netem %v", pid, experimentDetails.NetworkInterface, netemCommands)
			cmd = exec.Command("/bin/bash", "-c", traffic)
			out, err = cmd.CombinedOutput()
			log.Info(cmd.String())
			if err != nil {
				log.Error(string(out))
				return err
			}

			for _, ip := range uniqueIps {

				// redirect traffic to specific IP through band 3
				// It allows ipv4 addresses only
				if !strings.Contains(ip, ":") {
					tc := fmt.Sprintf("sudo nsenter -t %v -n tc filter add dev %v protocol ip parent 1:0 prio 3 u32 match ip dst %v flowid 1:3", pid, experimentDetails.NetworkInterface, ip)
					cmd = exec.Command("/bin/bash", "-c", tc)
					out, err = cmd.CombinedOutput()
					log.Info(cmd.String())
					if err != nil {
						log.Error(string(out))
						return err
					}
				}
			}
		}
	}
	return nil
}

// Killnetem kill the netem process for all the target containers
func Killnetem(PID int) error {

	tc := fmt.Sprintf("sudo nsenter -t %d -n tc qdisc delete dev eth0 root", PID)
	cmd := exec.Command("/bin/bash", "-c", tc)
	out, err := cmd.CombinedOutput()
	log.Info(cmd.String())

	if err != nil {
		log.Error(string(out))
		// ignoring err if qdisc process doesn't exist inside the target container
		if strings.Contains(string(out), qdiscNotFound) || strings.Contains(string(out), qdiscNoFileFound) {
			log.Warn("The network chaos process has already been removed")
			return nil
		}
		return err
	}

	return nil
}

//GetENV fetches all the env variables from the runner pod
func GetENV(experimentDetails *experimentTypes.ExperimentDetails) {
	experimentDetails.ExperimentName = Getenv("EXPERIMENT_NAME", "")
	experimentDetails.AppNS = Getenv("APP_NS", "")
	experimentDetails.TargetContainer = Getenv("APP_CONTAINER", "")
	experimentDetails.TargetPods = Getenv("APP_POD", "")
	experimentDetails.AppLabel = Getenv("APP_LABEL", "")
	experimentDetails.ChaosDuration, _ = strconv.Atoi(Getenv("TOTAL_CHAOS_DURATION", "30"))
	experimentDetails.ChaosNamespace = Getenv("CHAOS_NAMESPACE", "litmus")
	experimentDetails.EngineName = Getenv("CHAOS_ENGINE", "")
	experimentDetails.ChaosUID = clientTypes.UID(Getenv("CHAOS_UID", ""))
	experimentDetails.ChaosPodName = Getenv("POD_NAME", "")
	experimentDetails.ContainerRuntime = Getenv("CONTAINER_RUNTIME", "")
	experimentDetails.NetworkInterface = Getenv("NETWORK_INTERFACE", "eth0")
	experimentDetails.SocketPath = Getenv("SOCKET_PATH", "")
	experimentDetails.DestinationIPs = Getenv("DESTINATION_IPS", "")
}

// Getenv fetch the env and set the default value, if any
func Getenv(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		value = defaultValue
	}
	return value
}

// abortWatcher continuosly watch for the abort signals
func abortWatcher(targetPID int) {

	for {
		select {
		case <-abort:
			log.Info("[Chaos]: Killing process started because of terminated signal received")
			log.Info("Chaos Revert Started")
			// retry thrice for the chaos revert
			retry := 3
			for retry > 0 {
				if err = Killnetem(targetPID); err != nil {
					log.Errorf("unable to kill netem process, err :%v", err)
				}
				retry--
				time.Sleep(1 * time.Second)
			}
			log.Info("Chaos Revert Completed")
			os.Exit(1)
		}
	}
}
