/* Copyright (c) 2016 PaddlePaddle Authors All Rights Reserve.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

   http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
	 limitations under the License. */

// Controller is responsible to watch resource type "TrainingJob"
// event and parse "TrainingJob" into several other resources like
// "Job" and "ReplicaSet".

// Controller will manage "TrainingJob" creation and destruction while
// AutoScaler will scale the job to maximize the cluster resource usage.

// When controller starts, both event watching routine and resource
// monitoring and scaling routine should be started.

package edl

import (
	"encoding/json"
	"sync"

	log "github.com/inconshreveable/log15"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/kubernetes/pkg/api"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	edlresource "github.com/paddlepaddle/edl/pkg/resource"
)

// Controller for dispatching TrainingJob resource.
type Controller struct {
	client     *rest.RESTClient
	clientset  *kubernetes.Clientset
	autoscaler *Autoscaler
}

// New construct a new Controller struct
func New(c *rest.RESTClient, cs *kubernetes.Clientset, maxLoadDesired float64) (*Controller, error) {
	cluster := newCluster(cs)
	as := newAutoscaler(cluster,
		withMaxLoadDesired(maxLoadDesired))

	return &Controller{
		client:     c,
		clientset:  cs,
		autoscaler: as,
	}, nil
}

// Run start to watch kubernetes events and do handlers.
func (c *Controller) Run() {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		c.WatchTrainingJobs()
		wg.Done()
	}()
	go func() {
		c.autoscaler.Run()
		wg.Done()
	}()
	wg.Wait()
}

// WatchTrainingJobs moinitors trainingjobs resources.
func (c *Controller) WatchTrainingJobs() {
	source := cache.NewListWatchFromClient(
		c.client,
		edlresource.TrainingJobs,
		// TODO(helin): pass in namespace as an argument.
		api.NamespaceAll,
		fields.Everything())

	_, informer := cache.NewInformer(
		source,
		&edlresource.TrainingJob{},

		// TODO(helin): support resync. resync will eventually
		// happen even if the resyncPeriod parameter is set to
		// 0.

		// resyncPeriod: Every resyncPeriod, all resources in
		// the cache will retrigger events. Set to 0 to
		// disable the resync.
		0,

		// TrainingJob custom resource event handlers.
		cache.ResourceEventHandlerFuncs{
			AddFunc:    c.onAdd,
			UpdateFunc: c.onUpdate,
			DeleteFunc: c.onDelete,
		})

	informer.Run(make(chan struct{})) // A channel will never close.
}

func (c *Controller) onAdd(obj interface{}) {
	job := obj.(*edlresource.TrainingJob)
	log.Debug("TrainingJob resource added", "name", job.ObjectMeta.Name)
	c.autoscaler.OnAdd(job)

	// TODO(gongwb):open it when all are ready.
	// All-are-ready means:
	//  create trainjob from paddlectl
	//  scheduler can schedule trainjobs
	var parser DefaultJobParser
	m := parser.ParseToMaster(job)
	p := parser.ParseToPserver(job)
	t := parser.ParseToTrainer(job)

	b, _ := json.MarshalIndent(m, "", "   ")
	log.Debug("create master:" + string(b))

	b, _ = json.MarshalIndent(p, "", "   ")
	log.Debug("create pserver:" + string(b))

	b, _ = json.MarshalIndent(t, "", "   ")
	log.Debug("create trainer-job:" + string(b))

	// create all resources
	_, err := c.clientset.ExtensionsV1beta1().ReplicaSets(m.ObjectMeta.Namespace).Create(m)
	if err != nil {
		log.Error("create master", "error", err)
	}

	_, err = c.clientset.ExtensionsV1beta1().ReplicaSets(m.ObjectMeta.Namespace).Create(p)
	if err != nil {
		log.Error("create pserver", "error", err)
	}

	_, err = c.clientset.BatchV1().Jobs(t.ObjectMeta.Namespace).Create(t)
	if err != nil {
		log.Error("create trainer", "error", err)
	}
}

func (c *Controller) onUpdate(oldObj, newObj interface{}) {
	oldjob := oldObj.(*edlresource.TrainingJob)
	newjob := newObj.(*edlresource.TrainingJob)
	log.Debug("TrainingJob resource updated", "old name", oldjob.ObjectMeta.Name, "new name", newjob.ObjectMeta.Name)
	c.autoscaler.OnUpdate(newjob)
}

func (c *Controller) onDelete(obj interface{}) {
	job := obj.(*edlresource.TrainingJob)
	log.Debug("TrainingJob resource deleted", "name", job.ObjectMeta.Name)
	c.autoscaler.OnDel(job)
}
