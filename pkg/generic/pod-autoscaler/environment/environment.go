package environment

import (
	"os"
	"strconv"

	experimentTypes "github.com/litmuschaos/litmus-go/pkg/generic/pod-autoscaler/types"
	"github.com/litmuschaos/litmus-go/pkg/types"
	clientTypes "k8s.io/apimachinery/pkg/types"
)

//GetENV fetches all the env variables from the runner pod
func GetENV(experimentDetails *experimentTypes.ExperimentDetails) {
	experimentDetails.ExperimentName = Getenv("EXPERIMENT_NAME", "pod-autoscaler")
	experimentDetails.ChaosNamespace = Getenv("CHAOS_NAMESPACE", "litmus")
	experimentDetails.EngineName = Getenv("CHAOSENGINE", "")
	experimentDetails.ChaosDuration, _ = strconv.Atoi(Getenv("TOTAL_CHAOS_DURATION", "60"))
	experimentDetails.RampTime, _ = strconv.Atoi(Getenv("RAMP_TIME", "0"))
	experimentDetails.ChaosLib = Getenv("LIB", "litmus")
	experimentDetails.AppNS = Getenv("APP_NAMESPACE", "")
	experimentDetails.AppLabel = Getenv("APP_LABEL", "")
	experimentDetails.AppKind = Getenv("APP_KIND", "")
	experimentDetails.AppAffectPercentage, _ = strconv.Atoi(Getenv("APP_AFFECT_PERC", "100"))
	experimentDetails.Replicas, _ = strconv.Atoi(Getenv("REPLICA_COUNT", ""))
	experimentDetails.ChaosUID = clientTypes.UID(Getenv("CHAOS_UID", ""))
	experimentDetails.InstanceID = Getenv("INSTANCE_ID", "")
	experimentDetails.ChaosPodName = Getenv("POD_NAME", "")
	experimentDetails.AuxiliaryAppInfo = Getenv("AUXILIARY_APPINFO", "")
	experimentDetails.Delay, _ = strconv.Atoi(Getenv("STATUS_CHECK_DELAY", "2"))
	experimentDetails.Timeout, _ = strconv.Atoi(Getenv("STATUS_CHECK_TIMEOUT", "180"))
	experimentDetails.TargetContainer = Getenv("TARGET_CONTAINER", "")
}

// Getenv fetch the env and set the default value, if any
func Getenv(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		value = defaultValue
	}
	return value
}

//InitialiseChaosVariables initialise all the global variables
func InitialiseChaosVariables(chaosDetails *types.ChaosDetails, experimentDetails *experimentTypes.ExperimentDetails) {
	appDetails := types.AppDetails{}
	appDetails.AnnotationCheck, _ = strconv.ParseBool(Getenv("ANNOTATION_CHECK", "false"))
	appDetails.AnnotationKey = Getenv("ANNOTATION_KEY", "litmuschaos.io/chaos")
	appDetails.AnnotationValue = "true"
	appDetails.Kind = experimentDetails.AppKind
	appDetails.Label = experimentDetails.AppLabel
	appDetails.Namespace = experimentDetails.AppNS

	chaosDetails.ChaosNamespace = experimentDetails.ChaosNamespace
	chaosDetails.ChaosPodName = experimentDetails.ChaosPodName
	chaosDetails.ChaosUID = experimentDetails.ChaosUID
	chaosDetails.EngineName = experimentDetails.EngineName
	chaosDetails.ExperimentName = experimentDetails.ExperimentName
	chaosDetails.InstanceID = experimentDetails.InstanceID
	chaosDetails.ProbeImagePullPolicy = experimentDetails.LIBImagePullPolicy
	chaosDetails.AppDetail = appDetails
}
