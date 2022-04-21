package manager

import (
	"context"
	"github.com/hwameistor/improved-system/pkg/apis"
	apisv1alpha1 "github.com/hwameistor/improved-system/pkg/apis/hwameistor/v1alpha1"
	migratepkg "github.com/hwameistor/improved-system/pkg/migrate"
	"github.com/hwameistor/improved-system/pkg/replacedisk/node"
	"github.com/hwameistor/improved-system/pkg/utils"
	ldctr "github.com/hwameistor/local-disk-manager/pkg/controller/localdisk"
	"github.com/hwameistor/local-disk-manager/pkg/localdisk"
	log "github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mgrpkg "sigs.k8s.io/controller-runtime/pkg/manager"
)

// Infinitely retry
const maxRetries = 0

var (
	metricsHost                  = "0.0.0.0"
	metricsPort            int32 = 8384
	replaceDiskManager     apis.ReplaceDiskManager
	replaceDiskNodeManager apis.ReplaceDiskNodeManager
)

type manager struct {
	nodeName string

	namespace string

	apiClient client.Client

	rdhandler *ReplaceDiskHandler

	migrateCtr migratepkg.Controller

	localDiskController localdisk.Controller

	ldhandler *ldctr.LocalDiskHandler

	mgr mgrpkg.Manager

	cmdExec *lvmExecutor

	logger *log.Entry
}

//func Manager() (apis.ReplaceDiskManager, error) {
//	// Get a config to talk to the apiserver
//	cfg, err := config.GetConfig()
//	if err != nil {
//		log.Error(err, "")
//		os.Exit(1)
//	}
//
//	// Set default manager options
//	options := mgrpkg.Options{
//		MetricsBindAddress: fmt.Sprintf("%s:%d", metricsHost, metricsPort),
//	}
//
//	// Create a new manager to provide shared dependencies and start components
//	mgr, err := mgrpkg.New(cfg, options)
//	if err != nil {
//		log.Error(err, "")
//		os.Exit(1)
//	}
//	if replaceDiskManager == nil {
//		replaceDiskManager, err = New(mgr)
//		if err != nil {
//			log.Error(err, "")
//			return replaceDiskManager, err
//		}
//	}
//	return replaceDiskManager, nil
//}

// New replacedisk manager
func New(mgr mgrpkg.Manager) (apis.ReplaceDiskManager, error) {
	var recorder record.EventRecorder
	return &manager{
		nodeName:            utils.GetNodeName(),
		namespace:           utils.GetNamespace(),
		apiClient:           mgr.GetClient(),
		rdhandler:           NewReplaceDiskHandler(mgr.GetClient(), recorder),
		migrateCtr:          migratepkg.NewController(mgr),
		mgr:                 mgr,
		localDiskController: localdisk.NewController(mgr),
		ldhandler:           ldctr.NewLocalDiskHandler(mgr.GetClient(), recorder),
		cmdExec:             NewLVMExecutor(),
		logger:              log.WithField("Module", "ReplaceDisk"),
	}, nil
}

func (m *manager) Run(stopCh <-chan struct{}) {
	go m.startReplaceDiskTaskWorker(stopCh)
}

func (m *manager) ReplaceDiskNodeManager() apis.ReplaceDiskNodeManager {
	if replaceDiskNodeManager == nil {
		replaceDiskNodeManager = node.NewReplaceDiskNodeManager()
	}
	return replaceDiskNodeManager
}

// ReplaceDiskHandler
type ReplaceDiskHandler struct {
	client.Client
	record.EventRecorder
	ReplaceDisk apisv1alpha1.ReplaceDisk
}

// NewReplaceDiskHandler
func NewReplaceDiskHandler(client client.Client, recorder record.EventRecorder) *ReplaceDiskHandler {
	return &ReplaceDiskHandler{
		Client:        client,
		EventRecorder: recorder,
	}
}

// ListReplaceDisk
func (rdHandler *ReplaceDiskHandler) ListReplaceDisk() (*apisv1alpha1.ReplaceDiskList, error) {
	list := &apisv1alpha1.ReplaceDiskList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ReplaceDisk",
			APIVersion: "v1alpha1",
		},
	}

	err := rdHandler.List(context.TODO(), list)
	return list, err
}

// GetReplaceDisk
func (rdHandler *ReplaceDiskHandler) GetReplaceDisk(key client.ObjectKey) (*apisv1alpha1.ReplaceDisk, error) {
	rd := &apisv1alpha1.ReplaceDisk{}
	if err := rdHandler.Get(context.Background(), key, rd); err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	return rd, nil
}

// UpdateReplaceDiskStatus
func (rdHandler *ReplaceDiskHandler) UpdateReplaceDiskStatus(status apisv1alpha1.ReplaceDiskStatus) error {
	rdHandler.ReplaceDisk.Status.OldDiskReplaceStatus = status.OldDiskReplaceStatus
	rdHandler.ReplaceDisk.Status.NewDiskReplaceStatus = status.NewDiskReplaceStatus
	return rdHandler.Status().Update(context.Background(), &rdHandler.ReplaceDisk)
}

// Refresh
func (rdHandler *ReplaceDiskHandler) Refresh() error {
	rd, err := rdHandler.GetReplaceDisk(client.ObjectKey{Name: rdHandler.ReplaceDisk.GetName(), Namespace: rdHandler.ReplaceDisk.GetNamespace()})
	if err != nil {
		return err
	}
	rdHandler.SetReplaceDisk(*rd.DeepCopy())
	return nil
}

// SetReplaceDisk
func (rdHandler *ReplaceDiskHandler) SetReplaceDisk(rd apisv1alpha1.ReplaceDisk) *ReplaceDiskHandler {
	rdHandler.ReplaceDisk = rd
	return rdHandler
}

// SetReplaceDisk
func (rdHandler *ReplaceDiskHandler) SetMigrateVolumeNames(volumeNames []string) *ReplaceDiskHandler {
	rdHandler.ReplaceDisk.Status.MigrateVolumeNames = volumeNames
	return rdHandler
}

// ReplaceDiskStage
func (rdHandler *ReplaceDiskHandler) ReplaceDiskStage() apisv1alpha1.ReplaceDiskStage {
	return rdHandler.ReplaceDisk.Spec.ReplaceDiskStage
}

// ReplaceDiskStatus
func (rdHandler *ReplaceDiskHandler) ReplaceDiskStatus() apisv1alpha1.ReplaceDiskStatus {
	return rdHandler.ReplaceDisk.Status
}

// SetReplaceDiskStage
func (rdHandler *ReplaceDiskHandler) SetReplaceDiskStage(stage apisv1alpha1.ReplaceDiskStage) *ReplaceDiskHandler {
	rdHandler.ReplaceDisk.Spec.ReplaceDiskStage = stage
	return rdHandler
}

// UpdateReplaceDiskCR
func (rdHandler *ReplaceDiskHandler) UpdateReplaceDiskCR() error {
	return rdHandler.Update(context.Background(), &rdHandler.ReplaceDisk)
}
