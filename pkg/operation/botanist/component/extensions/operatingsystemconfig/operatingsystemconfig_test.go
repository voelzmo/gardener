// Copyright (c) 2021 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package operatingsystemconfig_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	gardencorev1alpha1 "github.com/gardener/gardener/pkg/apis/core/v1alpha1"
	gardencorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/extensions"
	"github.com/gardener/gardener/pkg/logger"
	mockclient "github.com/gardener/gardener/pkg/mock/controller-runtime/client"
	mocktime "github.com/gardener/gardener/pkg/mock/go/time"
	. "github.com/gardener/gardener/pkg/operation/botanist/component/extensions/operatingsystemconfig"
	"github.com/gardener/gardener/pkg/operation/botanist/component/extensions/operatingsystemconfig/original/components"
	"github.com/gardener/gardener/pkg/utils"
	gutil "github.com/gardener/gardener/pkg/utils/gardener"
	"github.com/gardener/gardener/pkg/utils/imagevector"
	"github.com/gardener/gardener/pkg/utils/test"
	. "github.com/gardener/gardener/pkg/utils/test/matchers"

	"github.com/Masterminds/semver"
	"github.com/golang/mock/gomock"
	"github.com/hashicorp/go-multierror"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("OperatingSystemConfig", func() {
	Describe("> Interface", func() {
		const namespace = "test-namespace"

		var (
			ctrl             *gomock.Controller
			c                client.Client
			defaultDepWaiter Interface

			ctx     context.Context
			values  *Values
			log     logrus.FieldLogger
			fakeErr = fmt.Errorf("some random error")

			mockNow *mocktime.MockNow
			now     time.Time

			apiServerURL            = "https://url-to-apiserver"
			caBundle                = pointer.String("ca-bundle")
			clusterDNSAddress       = "cluster-dns"
			clusterDomain           = "cluster-domain"
			images                  = map[string]*imagevector.Image{"foo": {}}
			kubeletCACertificate    = "kubelet-ca"
			kubeletCLIFlags         = components.ConfigurableKubeletCLIFlags{}
			kubeletConfigParameters = components.ConfigurableKubeletConfigParameters{}
			kubeletDataVolumeName   = "foo"
			machineTypes            []gardencorev1beta1.MachineType
			sshPublicKeys           = []string{"ssh-public-key", "ssh-public-key-b"}

			ccdUnitContent = "ccd-unit-content"

			downloaderConfigFn = func(cloudConfigUserDataSecretName, apiServerURL string) ([]extensionsv1alpha1.Unit, []extensionsv1alpha1.File, error) {
				return []extensionsv1alpha1.Unit{
						{Name: cloudConfigUserDataSecretName},
						{
							Name:    "cloud-config-downloader.service",
							Content: &ccdUnitContent,
						},
					},
					[]extensionsv1alpha1.File{
						{Path: apiServerURL},
					},
					nil
			}
			originalConfigFn = func(cctx components.Context) ([]extensionsv1alpha1.Unit, []extensionsv1alpha1.File, error) {
				return []extensionsv1alpha1.Unit{
						{Name: *cctx.CABundle},
						{Name: cctx.ClusterDNSAddress},
						{Name: cctx.ClusterDomain},
						{Name: string(cctx.CRIName)},
					},
					[]extensionsv1alpha1.File{
						{Path: fmt.Sprintf("%s", cctx.Images)},
						{Path: cctx.KubeletCACertificate},
						{Path: fmt.Sprintf("%v", cctx.KubeletCLIFlags)},
						{Path: fmt.Sprintf("%v", cctx.KubeletConfigParameters)},
						{Path: *cctx.KubeletDataVolumeName},
						{Path: cctx.KubernetesVersion.String()},
						{Path: fmt.Sprintf("%s", cctx.SSHPublicKeys)},
					},
					nil
			}

			worker1Name = "worker1"
			worker2Name = "worker2"
			workers     = []gardencorev1beta1.Worker{
				{
					Name: worker1Name,
					Machine: gardencorev1beta1.Machine{
						Image: &gardencorev1beta1.ShootMachineImage{
							Name:           "type1",
							ProviderConfig: &runtime.RawExtension{Raw: []byte(`{"foo":"bar"}`)},
						},
					},
					KubeletDataVolumeName: &kubeletDataVolumeName,
				},
				{
					Name: worker2Name,
					Machine: gardencorev1beta1.Machine{
						Image: &gardencorev1beta1.ShootMachineImage{
							Name: "type2",
						},
					},
					CRI: &gardencorev1beta1.CRI{
						Name: gardencorev1beta1.CRINameContainerD,
					},
					KubeletDataVolumeName: &kubeletDataVolumeName,
				},
			}
			empty    *extensionsv1alpha1.OperatingSystemConfig
			expected []*extensionsv1alpha1.OperatingSystemConfig
		)

		BeforeEach(func() {
			ctrl = gomock.NewController(GinkgoT())
			mockNow = mocktime.NewMockNow(ctrl)
			now = time.Now()

			ctx = context.TODO()
			log = logger.NewNopLogger()

			s := runtime.NewScheme()
			Expect(extensionsv1alpha1.AddToScheme(s)).To(Succeed())
			Expect(kubernetesfake.AddToScheme(s)).To(Succeed())
			c = fake.NewClientBuilder().WithScheme(s).Build()

			values = &Values{
				Namespace:         namespace,
				Workers:           workers,
				KubernetesVersion: semver.MustParse("1.2.3"),
				DownloaderValues: DownloaderValues{
					APIServerURL: apiServerURL,
				},
				OriginalValues: OriginalValues{
					CABundle:                caBundle,
					ClusterDNSAddress:       clusterDNSAddress,
					ClusterDomain:           clusterDomain,
					Images:                  images,
					KubeletCACertificate:    kubeletCACertificate,
					KubeletConfigParameters: kubeletConfigParameters,
					KubeletCLIFlags:         kubeletCLIFlags,
					MachineTypes:            machineTypes,
					SSHPublicKeys:           sshPublicKeys,
				},
			}

			empty = &extensionsv1alpha1.OperatingSystemConfig{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
			}

			expected = make([]*extensionsv1alpha1.OperatingSystemConfig, 0, 2*len(workers))
			for _, worker := range workers {
				var (
					criName   extensionsv1alpha1.CRIName
					criConfig *extensionsv1alpha1.CRIConfig
				)
				if worker.CRI != nil {
					criName = extensionsv1alpha1.CRIName(worker.CRI.Name)
					criConfig = &extensionsv1alpha1.CRIConfig{Name: extensionsv1alpha1.CRIName(worker.CRI.Name)}
				} else {
					criName = extensionsv1alpha1.CRINameDocker
				}

				downloaderUnits, downloaderFiles, _ := downloaderConfigFn(
					"cloud-config-"+worker.Name+"-77ac3",
					apiServerURL,
				)
				originalUnits, originalFiles, _ := originalConfigFn(components.Context{
					CABundle:                caBundle,
					ClusterDNSAddress:       clusterDNSAddress,
					ClusterDomain:           clusterDomain,
					CRIName:                 criName,
					Images:                  images,
					KubeletCACertificate:    kubeletCACertificate,
					KubeletCLIFlags:         kubeletCLIFlags,
					KubeletConfigParameters: kubeletConfigParameters,
					KubeletDataVolumeName:   &kubeletDataVolumeName,
					KubernetesVersion:       values.KubernetesVersion,
					SSHPublicKeys:           sshPublicKeys,
				})

				oscDownloader := &extensionsv1alpha1.OperatingSystemConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cloud-config-" + worker.Name + "-77ac3-downloader",
						Namespace: namespace,
						Annotations: map[string]string{
							v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationReconcile,
							v1beta1constants.GardenerTimestamp: now.UTC().String(),
						},
					},
					Spec: extensionsv1alpha1.OperatingSystemConfigSpec{
						DefaultSpec: extensionsv1alpha1.DefaultSpec{
							Type:           worker.Machine.Image.Name,
							ProviderConfig: worker.Machine.Image.ProviderConfig,
						},
						Purpose:   extensionsv1alpha1.OperatingSystemConfigPurposeProvision,
						CRIConfig: criConfig,
						Units:     downloaderUnits,
						Files:     downloaderFiles,
					},
				}

				oscOriginal := &extensionsv1alpha1.OperatingSystemConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cloud-config-" + worker.Name + "-77ac3-original",
						Namespace: namespace,
						Annotations: map[string]string{
							v1beta1constants.GardenerOperation: v1beta1constants.GardenerOperationReconcile,
							v1beta1constants.GardenerTimestamp: now.UTC().String(),
						},
					},
					Spec: extensionsv1alpha1.OperatingSystemConfigSpec{
						DefaultSpec: extensionsv1alpha1.DefaultSpec{
							Type:           worker.Machine.Image.Name,
							ProviderConfig: worker.Machine.Image.ProviderConfig,
						},
						Purpose:              extensionsv1alpha1.OperatingSystemConfigPurposeReconcile,
						CRIConfig:            criConfig,
						ReloadConfigFilePath: pointer.String("/var/lib/cloud-config-downloader/downloads/cloud_config"),
						Units:                originalUnits,
						Files: append(append(originalFiles, downloaderFiles...), extensionsv1alpha1.File{
							Path:        "/etc/systemd/system/cloud-config-downloader.service",
							Permissions: pointer.Int32(0644),
							Content: extensionsv1alpha1.FileContent{
								Inline: &extensionsv1alpha1.FileContentInline{
									Encoding: "b64",
									Data:     utils.EncodeBase64([]byte(ccdUnitContent)),
								},
							},
						}),
					},
				}

				expected = append(expected, oscDownloader, oscOriginal)
			}

			defaultDepWaiter = New(log, c, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
		})

		AfterEach(func() {
			ctrl.Finish()
		})

		Describe("#Deploy", func() {
			It("should successfully deploy all extensions resources", func() {
				defer test.WithVars(
					&TimeNow, mockNow.Do,
					&DownloaderConfigFn, downloaderConfigFn,
					&OriginalConfigFn, originalConfigFn,
				)()

				mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

				Expect(defaultDepWaiter.Deploy(ctx)).To(Succeed())

				for _, e := range expected {
					actual := &extensionsv1alpha1.OperatingSystemConfig{}
					Expect(c.Get(ctx, client.ObjectKey{Name: e.Name, Namespace: e.Namespace}, actual)).To(Succeed())

					obj := e.DeepCopy()
					obj.TypeMeta.APIVersion = extensionsv1alpha1.SchemeGroupVersion.String()
					obj.TypeMeta.Kind = extensionsv1alpha1.OperatingSystemConfigResource
					obj.ResourceVersion = "1"

					Expect(actual).To(Equal(obj))
				}
			})
		})

		Describe("#Restore", func() {
			var (
				stateDownloader = []byte(`{"dummy":"state downloader"}`)
				stateOriginal   = []byte(`{"dummy":"state original"}`)
				shootState      *gardencorev1alpha1.ShootState
			)

			BeforeEach(func() {
				extensions := make([]gardencorev1alpha1.ExtensionResourceState, 0, 2*len(workers))
				for _, worker := range workers {
					extensions = append(extensions,
						gardencorev1alpha1.ExtensionResourceState{
							Name:    pointer.String("cloud-config-" + worker.Name + "-77ac3-downloader"),
							Kind:    extensionsv1alpha1.OperatingSystemConfigResource,
							Purpose: pointer.String(string(extensionsv1alpha1.OperatingSystemConfigPurposeProvision)),
							State:   &runtime.RawExtension{Raw: stateDownloader},
						},
						gardencorev1alpha1.ExtensionResourceState{
							Name:    pointer.String("cloud-config-" + worker.Name + "-77ac3-original"),
							Kind:    extensionsv1alpha1.OperatingSystemConfigResource,
							Purpose: pointer.String(string(extensionsv1alpha1.OperatingSystemConfigPurposeReconcile)),
							State:   &runtime.RawExtension{Raw: stateOriginal},
						},
					)
				}
				shootState = &gardencorev1alpha1.ShootState{
					Spec: gardencorev1alpha1.ShootStateSpec{
						Extensions: extensions,
					},
				}
			})

			It("should properly restore the extensions state if it exists", func() {
				defer test.WithVars(
					&DownloaderConfigFn, downloaderConfigFn,
					&OriginalConfigFn, originalConfigFn,
					&TimeNow, mockNow.Do,
					&extensions.TimeNow, mockNow.Do,
				)()
				mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

				mc := mockclient.NewMockClient(ctrl)
				mc.EXPECT().Status().Return(mc).AnyTimes()

				for i := range expected {
					var state []byte
					if strings.HasSuffix(expected[i].Name, "downloader") {
						state = stateDownloader
					} else {
						state = stateOriginal
					}

					emptyWithName := empty.DeepCopy()
					emptyWithName.SetName(expected[i].GetName())
					mc.EXPECT().Get(ctx, client.ObjectKeyFromObject(emptyWithName), gomock.AssignableToTypeOf(emptyWithName)).
						Return(apierrors.NewNotFound(extensionsv1alpha1.Resource("operatingsystemconfigs"), emptyWithName.GetName()))

					// deploy with wait-for-state annotation
					obj := expected[i].DeepCopy()
					metav1.SetMetaDataAnnotation(&obj.ObjectMeta, "gardener.cloud/operation", "wait-for-state")
					metav1.SetMetaDataAnnotation(&obj.ObjectMeta, "gardener.cloud/timestamp", now.UTC().String())
					obj.TypeMeta = metav1.TypeMeta{}
					mc.EXPECT().Create(ctx, test.HasObjectKeyOf(obj)).
						DoAndReturn(func(ctx context.Context, actual client.Object, opts ...client.CreateOption) error {
							Expect(actual).To(DeepEqual(obj))
							return nil
						})

					// restore state
					expectedWithState := obj.DeepCopy()
					expectedWithState.Status.State = &runtime.RawExtension{Raw: state}
					test.EXPECTPatch(ctx, mc, expectedWithState, obj, types.MergePatchType)

					// annotate with restore annotation
					expectedWithRestore := expectedWithState.DeepCopy()
					metav1.SetMetaDataAnnotation(&expectedWithRestore.ObjectMeta, "gardener.cloud/operation", "restore")
					test.EXPECTPatch(ctx, mc, expectedWithRestore, expectedWithState, types.MergePatchType)
				}

				defaultDepWaiter = New(log, mc, values, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
				Expect(defaultDepWaiter.Restore(ctx, shootState)).To(Succeed())
			})
		})

		Describe("#Wait", func() {
			It("should return error when no resources are found", func() {
				Expect(defaultDepWaiter.Wait(ctx)).To(MatchError(ContainSubstring("not found")))
			})

			It("should return error when resource is not ready", func() {
				errDescription := "Some error"

				for i := range expected {
					expected[i].Status.LastError = &gardencorev1beta1.LastError{
						Description: errDescription,
					}
					Expect(c.Create(ctx, expected[i])).To(Succeed())
				}

				Expect(defaultDepWaiter.Wait(ctx)).To(MatchError(ContainSubstring("error during reconciliation: " + errDescription)))
			})

			It("should return error when status does not contain cloud config information", func() {
				for i := range expected {
					// remove operation annotation
					expected[i].ObjectMeta.Annotations = map[string]string{}
					// set last operation
					expected[i].Status.LastOperation = &gardencorev1beta1.LastOperation{
						State: gardencorev1beta1.LastOperationStateSucceeded,
					}
					Expect(c.Create(ctx, expected[i])).To(Succeed())
				}

				Expect(defaultDepWaiter.Wait(ctx)).To(MatchError(ContainSubstring("no cloud config information provided in status")))
			})

			It("should return error if we haven't observed the latest timestamp annotation", func() {
				defer test.WithVars(
					&TimeNow, mockNow.Do,
					&DownloaderConfigFn, downloaderConfigFn,
					&OriginalConfigFn, originalConfigFn,
				)()
				mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

				By("deploy")
				// Deploy should fill internal state with the added timestamp annotation
				Expect(defaultDepWaiter.Deploy(ctx)).To(Succeed())

				By("patch object")
				for i := range expected {
					patch := client.MergeFrom(expected[i].DeepCopy())
					// remove operation annotation, add old timestamp annotation
					expected[i].ObjectMeta.Annotations = map[string]string{
						v1beta1constants.GardenerTimestamp: now.Add(-time.Millisecond).UTC().String(),
					}
					// set last operation
					expected[i].Status.LastOperation = &gardencorev1beta1.LastOperation{
						State: gardencorev1beta1.LastOperationStateSucceeded,
					}
					// set cloud-config secret information
					expected[i].Status.CloudConfig = &extensionsv1alpha1.CloudConfig{
						SecretRef: corev1.SecretReference{
							Name:      "cc-" + expected[i].Name,
							Namespace: expected[i].Name,
						},
					}
					// set other status fields
					expected[i].Status.Command = pointer.String("foo-" + expected[i].Name)
					expected[i].Status.Units = []string{"bar-" + expected[i].Name, "baz-" + expected[i].Name}
					Expect(c.Patch(ctx, expected[i], patch)).ToNot(HaveOccurred(), "patching operatingsystemconfig succeeds")

					// create cloud-config secret
					ccSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "cc-" + expected[i].Name,
							Namespace: expected[i].Name,
						},
						Data: map[string][]byte{
							"cloud_config": []byte("foobar-" + expected[i].Name),
						},
					}
					Expect(c.Create(ctx, ccSecret)).To(Succeed())
				}

				By("wait")
				Expect(defaultDepWaiter.Wait(ctx)).NotTo(Succeed(), "operatingsystemconfig indicates error")
			})

			It("should return no error when it's ready", func() {
				defer test.WithVars(
					&TimeNow, mockNow.Do,
					&DownloaderConfigFn, downloaderConfigFn,
					&OriginalConfigFn, originalConfigFn,
				)()
				mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

				By("deploy")
				// Deploy should fill internal state with the added timestamp annotation
				Expect(defaultDepWaiter.Deploy(ctx)).To(Succeed())

				By("patch object")
				for i := range expected {
					patch := client.MergeFrom(expected[i].DeepCopy())
					// remove operation annotation, add up-to-date timestamp annotation
					expected[i].ObjectMeta.Annotations = map[string]string{
						v1beta1constants.GardenerTimestamp: now.UTC().String(),
					}
					// set last operation
					expected[i].Status.LastOperation = &gardencorev1beta1.LastOperation{
						State: gardencorev1beta1.LastOperationStateSucceeded,
					}
					// set cloud-config secret information
					expected[i].Status.CloudConfig = &extensionsv1alpha1.CloudConfig{
						SecretRef: corev1.SecretReference{
							Name:      "cc-" + expected[i].Name,
							Namespace: expected[i].Name,
						},
					}
					// set other status fields
					expected[i].Status.Command = pointer.String("foo-" + expected[i].Name)
					expected[i].Status.Units = []string{"bar-" + expected[i].Name, "baz-" + expected[i].Name}
					Expect(c.Patch(ctx, expected[i], patch)).ToNot(HaveOccurred(), "patching operatingsystemconfig succeeds")

					// create cloud-config secret
					ccSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "cc-" + expected[i].Name,
							Namespace: expected[i].Name,
						},
						Data: map[string][]byte{
							"cloud_config": []byte("foobar-" + expected[i].Name),
						},
					}
					Expect(c.Create(ctx, ccSecret)).To(Succeed())
				}

				By("wait")
				Expect(defaultDepWaiter.Wait(ctx)).To(Succeed(), "operatingsystemconfig is ready")
			})
		})

		Describe("WorkerNameToOperatingSystemConfigsMap", func() {
			It("should return the correct result from the Wait operation", func() {
				for i := range expected {
					// remove operation annotation
					expected[i].ObjectMeta.Annotations = map[string]string{}
					// set last operation
					expected[i].Status.LastOperation = &gardencorev1beta1.LastOperation{
						State: gardencorev1beta1.LastOperationStateSucceeded,
					}
					// set cloud-config secret information
					expected[i].Status.CloudConfig = &extensionsv1alpha1.CloudConfig{
						SecretRef: corev1.SecretReference{
							Name:      "cc-" + expected[i].Name,
							Namespace: expected[i].Name,
						},
					}
					// set other status fields
					expected[i].Status.Command = pointer.String("foo-" + expected[i].Name)
					expected[i].Status.Units = []string{"bar-" + expected[i].Name, "baz-" + expected[i].Name}
					Expect(c.Create(ctx, expected[i])).To(Succeed())

					// create cloud-config secret
					ccSecret := &corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "cc-" + expected[i].Name,
							Namespace: expected[i].Name,
						},
						Data: map[string][]byte{
							"cloud_config": []byte("foobar-" + expected[i].Name),
						},
					}
					Expect(c.Create(ctx, ccSecret)).To(Succeed())
				}

				Expect(defaultDepWaiter.Wait(ctx)).To(Succeed())
				Expect(defaultDepWaiter.WorkerNameToOperatingSystemConfigsMap()).To(Equal(map[string]*OperatingSystemConfigs{
					worker1Name: {
						Downloader: Data{
							Content: "foobar-cloud-config-" + worker1Name + "-77ac3-downloader",
							Command: pointer.String("foo-cloud-config-" + worker1Name + "-77ac3-downloader"),
							Units: []string{
								"bar-cloud-config-" + worker1Name + "-77ac3-downloader",
								"baz-cloud-config-" + worker1Name + "-77ac3-downloader",
							},
						},
						Original: Data{
							Content: "foobar-cloud-config-" + worker1Name + "-77ac3-original",
							Command: pointer.String("foo-cloud-config-" + worker1Name + "-77ac3-original"),
							Units: []string{
								"bar-cloud-config-" + worker1Name + "-77ac3-original",
								"baz-cloud-config-" + worker1Name + "-77ac3-original",
							},
						},
					},
					worker2Name: {
						Downloader: Data{
							Content: "foobar-cloud-config-" + worker2Name + "-77ac3-downloader",
							Command: pointer.String("foo-cloud-config-" + worker2Name + "-77ac3-downloader"),
							Units: []string{
								"bar-cloud-config-" + worker2Name + "-77ac3-downloader",
								"baz-cloud-config-" + worker2Name + "-77ac3-downloader",
							},
						},
						Original: Data{
							Content: "foobar-cloud-config-" + worker2Name + "-77ac3-original",
							Command: pointer.String("foo-cloud-config-" + worker2Name + "-77ac3-original"),
							Units: []string{
								"bar-cloud-config-" + worker2Name + "-77ac3-original",
								"baz-cloud-config-" + worker2Name + "-77ac3-original",
							},
						},
					},
				}))
			})
		})

		Describe("#Destroy", func() {
			It("should not return error when not found", func() {
				Expect(defaultDepWaiter.Destroy(ctx)).To(Succeed())
			})

			It("should not return error when deleted successfully", func() {
				Expect(c.Create(ctx, expected[0])).To(Succeed())
				Expect(defaultDepWaiter.Destroy(ctx)).To(Succeed())
			})

			It("should return error if not deleted successfully", func() {
				defer test.WithVars(
					&extensions.TimeNow, mockNow.Do,
					&gutil.TimeNow, mockNow.Do,
				)()
				mockNow.EXPECT().Do().Return(now.UTC()).AnyTimes()

				expectedOSC := extensionsv1alpha1.OperatingSystemConfig{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "osc1",
						Namespace: namespace,
						Annotations: map[string]string{
							gutil.ConfirmationDeletion:         "true",
							v1beta1constants.GardenerTimestamp: now.UTC().String(),
						},
					},
				}

				mc := mockclient.NewMockClient(ctrl)
				// check if the operatingsystemconfigs exist
				mc.EXPECT().List(ctx, gomock.AssignableToTypeOf(&extensionsv1alpha1.OperatingSystemConfigList{}), client.InNamespace(namespace)).SetArg(1, extensionsv1alpha1.OperatingSystemConfigList{Items: []extensionsv1alpha1.OperatingSystemConfig{expectedOSC}})
				// add deletion confirmation and Timestamp annotation
				mc.EXPECT().Patch(ctx, gomock.AssignableToTypeOf(&extensionsv1alpha1.OperatingSystemConfig{}), gomock.Any())
				mc.EXPECT().Delete(ctx, &expectedOSC).Return(fakeErr)

				defaultDepWaiter = New(log, mc, &Values{Namespace: namespace}, time.Millisecond, 250*time.Millisecond, 500*time.Millisecond)
				Expect(defaultDepWaiter.Destroy(ctx)).To(MatchError(multierror.Append(fakeErr)))
			})
		})

		Describe("#WaitCleanup", func() {
			It("should not return error when resources are removed", func() {
				Expect(defaultDepWaiter.WaitCleanup(ctx)).To(Succeed())
			})

			It("should not return error if resources exist but they don't have deletionTimestamp", func() {
				Expect(c.Create(ctx, expected[0])).To(Succeed())
				Expect(defaultDepWaiter.WaitCleanup(ctx)).To(Succeed())
			})

			It("should return error if resources with deletionTimestamp still exist", func() {
				timeNow := metav1.Now()
				expected[0].DeletionTimestamp = &timeNow
				Expect(c.Create(ctx, expected[0])).To(Succeed())
				Expect(defaultDepWaiter.WaitCleanup(ctx)).To(MatchError(ContainSubstring("is still present")))
			})
		})

		Describe("#Migrate", func() {
			It("should migrate the resources", func() {
				Expect(c.Create(ctx, expected[0])).To(Succeed())

				Expect(defaultDepWaiter.Migrate(ctx)).To(Succeed())

				annotatedResource := &extensionsv1alpha1.OperatingSystemConfig{}
				Expect(c.Get(ctx, client.ObjectKey{Name: expected[0].Name, Namespace: expected[0].Namespace}, annotatedResource)).To(Succeed())
				Expect(annotatedResource.Annotations[v1beta1constants.GardenerOperation]).To(Equal(v1beta1constants.GardenerOperationMigrate))
			})

			It("should not return error if resource does not exist", func() {
				Expect(defaultDepWaiter.Migrate(ctx)).To(Succeed())
			})
		})

		Describe("#WaitMigrate", func() {
			It("should not return error when resource is missing", func() {
				Expect(defaultDepWaiter.WaitMigrate(ctx)).To(Succeed())
			})

			It("should return error if resource is not yet migrated successfully", func() {
				expected[0].Status.LastError = &gardencorev1beta1.LastError{
					Description: "Some error",
				}
				expected[0].Status.LastOperation = &gardencorev1beta1.LastOperation{
					State: gardencorev1beta1.LastOperationStateError,
					Type:  gardencorev1beta1.LastOperationTypeMigrate,
				}

				Expect(c.Create(ctx, expected[0])).To(Succeed())
				Expect(defaultDepWaiter.WaitMigrate(ctx)).To(MatchError(ContainSubstring("is not Migrate=Succeeded")))
			})

			It("should not return error if resource gets migrated successfully", func() {
				expected[0].Status.LastError = nil
				expected[0].Status.LastOperation = &gardencorev1beta1.LastOperation{
					State: gardencorev1beta1.LastOperationStateSucceeded,
					Type:  gardencorev1beta1.LastOperationTypeMigrate,
				}

				Expect(c.Create(ctx, expected[0])).To(Succeed())
				Expect(defaultDepWaiter.WaitMigrate(ctx)).To(Succeed())
			})

			It("should return error if one resources is not migrated successfully and others are", func() {
				for i := range expected[1:] {
					expected[i].Status.LastError = nil
					expected[i].Status.LastOperation = &gardencorev1beta1.LastOperation{
						State: gardencorev1beta1.LastOperationStateSucceeded,
						Type:  gardencorev1beta1.LastOperationTypeMigrate,
					}
				}
				expected[0].Status.LastError = &gardencorev1beta1.LastError{
					Description: "Some error",
				}
				expected[0].Status.LastOperation = &gardencorev1beta1.LastOperation{
					State: gardencorev1beta1.LastOperationStateError,
					Type:  gardencorev1beta1.LastOperationTypeMigrate,
				}

				for _, e := range expected {
					Expect(c.Create(ctx, e)).To(Succeed())
				}
				Expect(defaultDepWaiter.WaitMigrate(ctx)).To(MatchError(ContainSubstring("is not Migrate=Succeeded")))
			})
		})

		Describe("#DeleteStaleResources", func() {
			It("should delete stale extensions resources", func() {
				newType := "new-type"

				staleOSC := expected[0].DeepCopy()
				staleOSC.Name = "new-name"
				Expect(c.Create(ctx, staleOSC)).To(Succeed())

				for _, e := range expected {
					Expect(c.Create(ctx, e)).To(Succeed())
				}

				Expect(defaultDepWaiter.DeleteStaleResources(ctx)).To(Succeed())

				oscList := &extensionsv1alpha1.OperatingSystemConfigList{}
				Expect(c.List(ctx, oscList)).To(Succeed())
				Expect(oscList.Items).To(HaveLen(2 * len(workers)))
				for _, item := range oscList.Items {
					Expect(item.Spec.Type).ToNot(Equal(newType))
				}
			})
		})
	})

	Describe("#Key", func() {
		var workerName = "foo"

		It("should return an empty string", func() {
			Expect(Key(workerName, nil)).To(BeEmpty())
		})

		It("should return the expected key", func() {
			Expect(Key(workerName, semver.MustParse("1.2.3"))).To(Equal("cloud-config-" + workerName + "-77ac3"))
		})
	})
})
