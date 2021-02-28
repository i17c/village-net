package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/containernetworking/plugins/pkg/node"
	"go.etcd.io/etcd/clientv3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
)

type Controller struct {
	etcdCli   *clientv3.Client
	clientSet *kubernetes.Clientset
	hostGw    *HostGw

	stopCh chan struct{}
}

func (c *Controller) Service() error {
	if err := c.InitNodeInfo(context.TODO()); err != nil {
		return err
	}

	go c.SyncRoute()

	<-c.stopCh

	return nil
}

func (c *Controller) SyncRoute() {
	result, err := c.etcdCli.Get(context.TODO(), EtcdKeyNodeInfo)
	if err != nil {
		klog.Errorf("query etcd failed: %s", err.Error())
		return
	}

	for _, kv := range result.Kvs {
		netInfo := &NodeNetInfo{}
		if err := json.Unmarshal(kv.Value, netInfo); err != nil {
			klog.Errorf("load node %s net info failed: %s", string(kv.Key), err.Error())
			continue
		}
		if err := c.hostGw.SyncNodeRoute(netInfo); err != nil {
			klog.Errorf("sync node %s net info failed: %s", string(kv.Key), err.Error())
		}
	}

	wc := c.etcdCli.Watch(context.Background(), EtcdKeyNodeInfo, clientv3.WithRev(result.Header.Revision))

	klog.V(1).Info("start watch")
	for {
		select {
		case watchBody := <-wc:
			for _, evt := range watchBody.Events {
				netInfo := &NodeNetInfo{}
				if err = json.Unmarshal(evt.Kv.Value, netInfo); err != nil {
					klog.Errorf("load node %s net info failed: %s", string(evt.Kv.Key), err.Error())
					continue
				}
				if err := c.hostGw.SyncNodeRoute(netInfo); err != nil {
					klog.Errorf("sync node %s net info failed: %s", string(evt.Kv.Key), err.Error())
				}
			}
		case <-c.stopCh:
			klog.V(1).Info("route sync close")
			return
		}
	}
}

func (c *Controller) InitNodeInfo(ctx context.Context) error {
	klog.V(4).Info("init node info")

	nodeInfo, err := c.clientSet.CoreV1().Nodes().Get(ctx, node.HostName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("list node failed: %s", err)
		return err
	}

	if nodeInfo == nil {
		klog.Errorf("node %s not found", node.HostName)
		return errors.New("node not found")
	}

	netInfo, err := GetNodeNetInfo()
	if err != nil {
		klog.Errorf("get node net info failed: %s", err.Error())
		return err
	}
	netInfo.CIDR = nodeInfo.Spec.PodCIDR

	nodeInfo.Annotations[LabelHostPodCIDR] = netInfo.CIDR
	nodeInfo.Annotations[LabelHostPublicIP] = netInfo.PublicIP
	nodeInfo.Annotations[LabelBridgeIFName] = netInfo.BridgeIf
	nodeInfo.Annotations[LabelMacVlanIFName] = netInfo.MacvlanIf

	_, err = c.clientSet.CoreV1().Nodes().Update(ctx, nodeInfo, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf("update node info failed: %s", err.Error())
		return err
	}

	k := fmt.Sprintf("%s/%s", EtcdKeyNodeInfo, netInfo.Hostname)
	v, _ := json.Marshal(netInfo)

	_, err = c.etcdCli.Put(ctx, k, string(v))
	if err != nil {
		klog.Error("record node info failed: %s", err.Error())
		return err
	}

	return nil
}
