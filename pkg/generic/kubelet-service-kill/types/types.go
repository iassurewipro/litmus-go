package types

import (
	corev1 "k8s.io/api/core/v1"
	clientTypes "k8s.io/apimachinery/pkg/types"
)

// ExperimentDetails is for collecting all the experiment-related details
type ExperimentDetails struct {
	ExperimentName     string
	EngineName         string
	ChaosDuration      int
	RampTime           int
	ChaosLib           string
	AppNS              string
	AppLabel           string
	AppKind            string
	ChaosUID           clientTypes.UID
	InstanceID         string
	ChaosNamespace     string
	ChaosPodName       string
	AuxiliaryAppInfo   string
	RunID              string
	TargetNode         string
	Timeout            int
	Delay              int
	Annotations        map[string]string
	LIBImage           string
	LIBImagePullPolicy string
	Resources          corev1.ResourceRequirements
	ImagePullSecrets   []corev1.LocalObjectReference
	TargetContainer    string
}
