/*
Copyright 2022.

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

package controllers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	statefulset "github.com/openstack-k8s-operators/lib-common/modules/common/statefulset"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"

	swiftv1beta1 "github.com/openstack-k8s-operators/swift-operator/api/v1beta1"
	swift "github.com/openstack-k8s-operators/swift-operator/pkg/swift"
)

// SwiftStorageReconciler reconciles a SwiftStorage object
type SwiftStorageReconciler struct {
	client.Client
	Scheme  *runtime.Scheme
	Log     logr.Logger
	Kclient kubernetes.Interface
}

//+kubebuilder:rbac:groups=swift.openstack.org,resources=swiftstorages,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=swift.openstack.org,resources=swiftstorages/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=swift.openstack.org,resources=swiftstorages/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the SwiftStorage object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *SwiftStorageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = r.Log.WithValues("swiftstorage", req.NamespacedName)

	instance := &swiftv1beta1.SwiftStorage{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			r.Log.Info("SwiftStorage resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		r.Log.Error(err, "Failed to get SwiftStorage")
		return ctrl.Result{}, err
	}

	helper, err := helper.NewHelper(
		instance,
		r.Client,
		r.Kclient,
		r.Scheme,
		r.Log,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	ls := swift.GetLabelsStorage()

	// Statefulset with all backend containers
	sset := statefulset.NewStatefulSet(getStorageStatefulSet(instance, ls), 5)
	ctrlResult, err := sset.CreateOrPatch(ctx, helper)
	if err != nil {
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}

	r.Log.Info(fmt.Sprintf("Reconciled SwiftStorage '%s' successfully", instance.Name))

	return ctrl.Result{}, nil
}

func getStorageVolumes(instance *swiftv1beta1.SwiftStorage) []corev1.Volume {
	return []corev1.Volume{
		{
			Name: "srv",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: "srv",
				},
			},
		},
		{
			Name: "config-data",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: instance.Name + "-config-data",
					},
				},
			},
		},
		{
			Name: "ring-data",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: instance.Spec.SwiftRingConfigMap,
					},
				},
			},
		},
		{
			Name: "config-data-merged",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{Medium: ""},
			},
		},
	}

}

func getStorageVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      "srv",
			MountPath: "/srv/node/d1",
			ReadOnly:  false,
		},
		{
			Name:      "config-data",
			MountPath: "/var/lib/config-data/default",
			ReadOnly:  true,
		},
		{
			Name:      "ring-data",
			MountPath: "/var/lib/config-data/rings",
			ReadOnly:  true,
		},
		{
			Name:      "config-data-merged",
			MountPath: "/etc/swift",
			ReadOnly:  false,
		},
	}
}

func getPorts(port int32, name string) []corev1.ContainerPort {
	return []corev1.ContainerPort{
		{
			ContainerPort: port,
			Name:          name,
		},
	}
}

func getStorageInitContainers(swiftstorage *swiftv1beta1.SwiftStorage) []corev1.Container {
	securityContext := swift.GetSecurityContext()

	return []corev1.Container{
		{
			Name:            "swift-init",
			Image:           swiftstorage.Spec.ContainerImageAccount,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/bin/sh", "-c", "cp -t /etc/swift/ /var/lib/config-data/default/* /var/lib/config-data/rings/*"},
		},
	}
}

func getStorageContainers(swiftstorage *swiftv1beta1.SwiftStorage) []corev1.Container {
	securityContext := swift.GetSecurityContext()

	return []corev1.Container{
		{
			Name:            "account-server",
			Image:           swiftstorage.Spec.ContainerImageAccount,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			Ports:           getPorts(swift.AccountServerPort, "account"),
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-account-server", "/etc/swift/account-server.conf", "-v"},
		},
		{
			Name:            "account-replicator",
			Image:           swiftstorage.Spec.ContainerImageAccount,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-account-replicator", "/etc/swift/account-server.conf", "-v"},
		},
		{
			Name:            "account-auditor",
			Image:           swiftstorage.Spec.ContainerImageAccount,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-account-auditor", "/etc/swift/account-server.conf", "-v"},
		},
		{
			Name:            "account-reaper",
			Image:           swiftstorage.Spec.ContainerImageAccount,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-account-reaper", "/etc/swift/account-server.conf", "-v"},
		},
		{
			Name:            "container-server",
			Image:           swiftstorage.Spec.ContainerImageContainer,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			Ports:           getPorts(swift.ContainerServerPort, "container"),
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-container-server", "/etc/swift/container-server.conf", "-v"},
		},
		{
			Name:            "container-replicator",
			Image:           swiftstorage.Spec.ContainerImageContainer,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-container-replicator", "/etc/swift/container-server.conf", "-v"},
		},
		{
			Name:            "container-auditor",
			Image:           swiftstorage.Spec.ContainerImageContainer,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-container-replicator", "/etc/swift/container-server.conf", "-v"},
		},
		{
			Name:            "container-updater",
			Image:           swiftstorage.Spec.ContainerImageContainer,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-container-replicator", "/etc/swift/container-server.conf", "-v"},
		},
		{
			Name:            "object-server",
			Image:           swiftstorage.Spec.ContainerImageObject,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			Ports:           getPorts(swift.ObjectServerPort, "object"),
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-object-server", "/etc/swift/object-server.conf", "-v"},
		},
		{
			Name:            "object-replicator",
			Image:           swiftstorage.Spec.ContainerImageObject,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-object-replicator", "/etc/swift/object-server.conf", "-v"},
		},
		{
			Name:            "object-auditor",
			Image:           swiftstorage.Spec.ContainerImageObject,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-object-replicator", "/etc/swift/object-server.conf", "-v"},
		},
		{
			Name:            "object-updater",
			Image:           swiftstorage.Spec.ContainerImageObject,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-object-replicator", "/etc/swift/object-server.conf", "-v"},
		},
		{
			Name:            "object-expirer",
			Image:           swiftstorage.Spec.ContainerImageProxy,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/swift-object-expirer", "/etc/swift/object-expirer.conf", "-v"},
		},
		{
			Name:            "rsync",
			Image:           swiftstorage.Spec.ContainerImageObject,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			Ports:           getPorts(swift.RsyncPort, "rsync"),
			VolumeMounts:    getStorageVolumeMounts(),
			Command:         []string{"/usr/bin/rsync", "--daemon", "--no-detach", "--config=/etc/swift/rsyncd.conf", "--log-file=/dev/stdout"},
		},
		{
			Name:            "memcached",
			Image:           swiftstorage.Spec.ContainerImageMemcached,
			ImagePullPolicy: corev1.PullIfNotPresent,
			SecurityContext: &securityContext,
			Ports:           getPorts(swift.MemcachedPort, "memcached"),
			Command:         []string{"/usr/bin/memcached", "-p", "11211", "-u", "memcached"},
		},
	}
}

func getStorageStatefulSet(
	swiftstorage *swiftv1beta1.SwiftStorage, labels map[string]string) *appsv1.StatefulSet {

	trueVal := true
	OnRootMismatch := corev1.FSGroupChangeOnRootMismatch
	user := int64(swift.RunAsUser)

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      swiftstorage.Name,
			Namespace: swiftstorage.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName: swiftstorage.Name,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Replicas: &swiftstorage.Spec.Replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						FSGroup:             &user,
						FSGroupChangePolicy: &OnRootMismatch,
						Sysctls: []corev1.Sysctl{{
							Name:  "net.ipv4.ip_unprivileged_port_start",
							Value: "873",
						}},
						RunAsNonRoot: &trueVal,
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Volumes:        getStorageVolumes(swiftstorage),
					InitContainers: getStorageInitContainers(swiftstorage),
					Containers:     getStorageContainers(swiftstorage),
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{
					Name: "srv",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					StorageClassName: &swiftstorage.Spec.StorageClassName,
					AccessModes: []corev1.PersistentVolumeAccessMode{
						corev1.ReadWriteOnce,
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}},
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *SwiftStorageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&swiftv1beta1.SwiftStorage{}).
		Owns(&appsv1.StatefulSet{}).
		Complete(r)
}