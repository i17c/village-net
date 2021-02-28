package controller

import (
	"go.etcd.io/etcd/clientv3"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
)

type Controller struct {
	etcdCli   *clientv3.Client
	clientSet *kubernetes.Clientset
}

func (c *Controller) Service() error {
	if err := c.InitNodeInfo(); err != nil {
		return err
	}

	go c.SyncRoute()

	return nil
}

func (c *Controller) SyncRoute() {
}

func (c *Controller) InitNodeInfo() error {
	klog.V(4).Info("init node info")

	return nil
}
