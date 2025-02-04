package v2alpha2

import (
	"context"
	"sort"
	"strings"

	emperror "emperror.dev/errors"
	"github.com/cisco-open/k8s-objectmatcher/patch"
	appsv2alpha2 "github.com/emqx/emqx-operator/apis/apps/v2alpha2"
	innerReq "github.com/emqx/emqx-operator/internal/requester"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type addRepl struct {
	*EMQXReconciler
}

func (a *addRepl) reconcile(ctx context.Context, instance *appsv2alpha2.EMQX, _ innerReq.RequesterInterface) subResult {
	if instance.Spec.ReplicantTemplate == nil {
		return subResult{}
	}
	if !instance.Status.IsConditionTrue(appsv2alpha2.CoreNodesReady) {
		return subResult{}
	}

	preRs := a.getNewReplicaSet(ctx, instance)
	if preRs.UID == "" {
		_ = ctrl.SetControllerReference(instance, preRs, a.Scheme)
		if err := a.Handler.Create(preRs); err != nil {
			if k8sErrors.IsAlreadyExists(emperror.Cause(err)) {
				if instance.Status.ReplicantNodesStatus.CollisionCount == nil {
					instance.Status.ReplicantNodesStatus.CollisionCount = pointer.Int32(0)
				}
				*instance.Status.ReplicantNodesStatus.CollisionCount++
				_ = a.Client.Status().Update(ctx, instance)
				return subResult{result: ctrl.Result{Requeue: true}}
			}
			return subResult{err: emperror.Wrap(err, "failed to create replicaSet")}
		}
		instance.Status.SetCondition(metav1.Condition{
			Type:    appsv2alpha2.ReplicantNodesProgressing,
			Status:  metav1.ConditionTrue,
			Reason:  "CreateNewReplicaSet",
			Message: "Create new replicaSet",
		})
		instance.Status.ReplicantNodesStatus.CurrentRevision = preRs.Labels[appsv2alpha2.PodTemplateHashLabelKey]
		_ = a.Client.Status().Update(ctx, instance)
	} else {
		storageRs := &appsv1.ReplicaSet{}
		_ = a.Client.Get(ctx, client.ObjectKeyFromObject(preRs), storageRs)
		patchResult, _ := a.Patcher.Calculate(storageRs, preRs,
			patch.IgnoreStatusFields(),
			patch.IgnoreVolumeClaimTemplateTypeMetaAndStatus(),
		)
		if !patchResult.IsEmpty() {
			logger := log.FromContext(ctx)
			logger.V(1).Info("got different statefulSet for EMQX core nodes, will update statefulSet", "patch", string(patchResult.Patch))

			_ = a.Handler.Update(preRs)
			instance.Status.SetCondition(metav1.Condition{
				Type:    appsv2alpha2.ReplicantNodesProgressing,
				Status:  metav1.ConditionTrue,
				Reason:  "CreateNewReplicaSet",
				Message: "Create new replicaSet",
			})
			_ = a.Client.Status().Update(ctx, instance)
		}
	}

	if err := a.sync(ctx, instance); err != nil {
		return subResult{err: emperror.Wrap(err, "failed to sync replicaSet")}
	}

	return subResult{}
}

func (a *addRepl) getNewReplicaSet(ctx context.Context, instance *appsv2alpha2.EMQX) *appsv1.ReplicaSet {
	preRs := generateReplicaSet(instance)
	podTemplateSpecHash := computeHash(preRs.Spec.Template.DeepCopy(), instance.Status.ReplicantNodesStatus.CollisionCount)
	preRs.Name = preRs.Name + "-" + podTemplateSpecHash
	preRs.Labels = appsv2alpha2.CloneAndAddLabel(preRs.Labels, appsv2alpha2.PodTemplateHashLabelKey, podTemplateSpecHash)
	preRs.Spec.Template.Labels = appsv2alpha2.CloneAndAddLabel(preRs.Spec.Template.Labels, appsv2alpha2.PodTemplateHashLabelKey, podTemplateSpecHash)
	preRs.Spec.Selector = appsv2alpha2.CloneSelectorAndAddLabel(preRs.Spec.Selector, appsv2alpha2.PodTemplateHashLabelKey, podTemplateSpecHash)

	currentRs, _ := getReplicaSetList(ctx, a.Client, instance)
	if currentRs == nil {
		return preRs
	}

	patchResult, _ := a.Patcher.Calculate(
		currentRs.DeepCopy(),
		preRs.DeepCopy(),
		justCheckPodTemplate(),
	)
	if patchResult.IsEmpty() {
		preRs.ObjectMeta = currentRs.ObjectMeta
		preRs.Spec.Template.ObjectMeta = currentRs.Spec.Template.ObjectMeta
		preRs.Spec.Selector = currentRs.Spec.Selector
		return preRs
	}
	logger := log.FromContext(ctx)
	logger.V(1).Info("got different pod template for EMQX replicant nodes, will create new replicaSet", "patch", string(patchResult.Patch))

	return preRs
}

func (a *addRepl) sync(ctx context.Context, instance *appsv2alpha2.EMQX) error {
	_, oldRsList := getReplicaSetList(ctx, a.Client, instance)
	if len(oldRsList) == 0 {
		return nil
	}

	oldest := oldRsList[0].DeepCopy()

	if pod := a.findCanBeDeletePod(ctx, instance, oldest); pod != nil {
		if pod.Annotations == nil {
			pod.Annotations = make(map[string]string)
		}
		// https://kubernetes.io/docs/concepts/workloads/controllers/replicaset/#pod-deletion-cost
		pod.Annotations["controller.kubernetes.io/pod-deletion-cost"] = "-99999"
		if err := a.Client.Patch(ctx, pod, client.MergeFrom(pod)); err != nil {
			return emperror.Wrap(err, "failed patch pod deletion cost")
		}

		oldest.Spec.Replicas = pointer.Int32(oldest.Status.Replicas - 1)
		if err := a.Client.Update(ctx, oldest); err != nil {
			return emperror.Wrap(err, "failed to scale down old replicaSet")
		}
		return nil
	}
	return nil
}

func (a *addRepl) findCanBeDeletePod(ctx context.Context, instance *appsv2alpha2.EMQX, old *appsv1.ReplicaSet) *corev1.Pod {
	if !canBeScaledDown(instance, appsv2alpha2.Ready, getEventList(ctx, a.Clientset, old)) {
		return nil
	}

	type podSessionCount struct {
		pod     *corev1.Pod
		edition string
		session int64
	}
	var podSessionCountList []*podSessionCount

	list := &corev1.PodList{}
	_ = a.Client.List(ctx, list, client.InNamespace(old.Namespace), client.MatchingLabels(old.Spec.Selector.MatchLabels))

	for _, node := range instance.Status.ReplicantNodesStatus.Nodes {
		for _, pod := range list.Items {
			host := strings.Split(node.Node[strings.Index(node.Node, "@")+1:], ":")[0]
			if pod.Status.PodIP == host {
				podSessionCountList = append(podSessionCountList, &podSessionCount{
					pod:     pod.DeepCopy(),
					edition: node.Edition,
					session: node.Session,
				})
			}
		}
	}

	sort.Slice(podSessionCountList, func(i, j int) bool {
		return podSessionCountList[i].session < podSessionCountList[j].session
	})

	if podSessionCountList[0].edition == "Enterprise" && podSessionCountList[0].session > 0 {
		return nil
	}

	return podSessionCountList[0].pod
}

func generateReplicaSet(instance *appsv2alpha2.EMQX) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ReplicaSet",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   instance.Namespace,
			Name:        instance.Spec.ReplicantTemplate.Name,
			Annotations: instance.Spec.ReplicantTemplate.Annotations,
			Labels:      instance.Spec.ReplicantTemplate.Labels,
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: instance.Spec.ReplicantTemplate.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: instance.Spec.ReplicantTemplate.Labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: instance.Spec.ReplicantTemplate.Labels,
				},
				Spec: corev1.PodSpec{
					ReadinessGates: []corev1.PodReadinessGate{
						{
							ConditionType: appsv2alpha2.PodOnServing,
						},
					},
					ImagePullSecrets: instance.Spec.ImagePullSecrets,
					SecurityContext:  instance.Spec.ReplicantTemplate.Spec.PodSecurityContext,
					Affinity:         instance.Spec.ReplicantTemplate.Spec.Affinity,
					Tolerations:      instance.Spec.ReplicantTemplate.Spec.ToleRations,
					NodeName:         instance.Spec.ReplicantTemplate.Spec.NodeName,
					NodeSelector:     instance.Spec.ReplicantTemplate.Spec.NodeSelector,
					InitContainers:   instance.Spec.ReplicantTemplate.Spec.InitContainers,
					Containers: append([]corev1.Container{
						{
							Name:            appsv2alpha2.DefaultContainerName,
							Image:           instance.Spec.Image,
							ImagePullPolicy: instance.Spec.ImagePullPolicy,
							Command:         instance.Spec.ReplicantTemplate.Spec.Command,
							Args:            instance.Spec.ReplicantTemplate.Spec.Args,
							Ports:           instance.Spec.ReplicantTemplate.Spec.Ports,
							Env: append([]corev1.EnvVar{
								{
									Name:  "EMQX_NODE__DB_ROLE",
									Value: "replicant",
								},
								{
									Name: "EMQX_HOST",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "status.podIP",
										},
									},
								},
								{
									Name: "EMQX_NODE__COOKIE",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: instance.NodeCookieNamespacedName().Name,
											},
											Key: "node_cookie",
										},
									},
								},
								{
									Name:  "EMQX_API_KEY__BOOTSTRAP_FILE",
									Value: `"/opt/emqx/data/bootstrap_user"`,
								},
							}, instance.Spec.ReplicantTemplate.Spec.Env...),
							EnvFrom:         instance.Spec.ReplicantTemplate.Spec.EnvFrom,
							Resources:       instance.Spec.ReplicantTemplate.Spec.Resources,
							SecurityContext: instance.Spec.ReplicantTemplate.Spec.ContainerSecurityContext,
							LivenessProbe:   instance.Spec.ReplicantTemplate.Spec.LivenessProbe,
							ReadinessProbe:  instance.Spec.ReplicantTemplate.Spec.ReadinessProbe,
							StartupProbe:    instance.Spec.ReplicantTemplate.Spec.StartupProbe,
							Lifecycle:       instance.Spec.ReplicantTemplate.Spec.Lifecycle,
							VolumeMounts: append([]corev1.VolumeMount{
								{
									Name:      "bootstrap-user",
									MountPath: "/opt/emqx/data/bootstrap_user",
									SubPath:   "bootstrap_user",
									ReadOnly:  true,
								},
								{
									Name:      "bootstrap-config",
									MountPath: "/opt/emqx/etc/emqx.conf",
									SubPath:   "emqx.conf",
									ReadOnly:  true,
								},
								{
									Name:      instance.Spec.ReplicantTemplate.Name + "-log",
									MountPath: "/opt/emqx/log",
								},
								{
									Name:      instance.Spec.ReplicantTemplate.Name + "-data",
									MountPath: "/opt/emqx/data",
								},
							}, instance.Spec.ReplicantTemplate.Spec.ExtraVolumeMounts...),
						},
					}, instance.Spec.ReplicantTemplate.Spec.ExtraContainers...),
					Volumes: append([]corev1.Volume{
						{
							Name: "bootstrap-user",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: instance.BootstrapUserNamespacedName().Name,
								},
							},
						},
						{
							Name: "bootstrap-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: instance.BootstrapConfigNamespacedName().Name,
									},
								},
							},
						},
						{
							Name: instance.Spec.ReplicantTemplate.Name + "-log",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: instance.Spec.ReplicantTemplate.Name + "-data",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					}, instance.Spec.ReplicantTemplate.Spec.ExtraVolumes...),
				},
			},
		},
	}
}
