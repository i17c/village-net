package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/containernetworking/plugins/pkg/node"
	"github.com/coreos/go-systemd/activation"
	"go.etcd.io/etcd/clientv3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"path/filepath"
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

func getListener(socketPath string) (net.Listener, error) {
	l, err := activation.Listeners()
	if err != nil {
		return nil, err
	}

	switch {
	case len(l) == 0:
		if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
			return nil, err
		}
		return net.Listen("unix", socketPath)

	case len(l) == 1:
		if l[0] == nil {
			return nil, fmt.Errorf("LISTEN_FDS=1 but no FD found")
		}
		return l[0], nil

	default:
		return nil, fmt.Errorf("Too many (%v) FDs passed through socket activation", len(l))
	}
}

func (c *Controller) runRPC(socketPath string) error {
	l, err := getListener(socketPath)
	if err != nil {
		return fmt.Errorf("Error getting listener: %v", err)
	}

	ipPool := newIpPool()
	rpc.Register(ipPool)
	rpc.HandleHTTP()
	http.Serve(l, nil)
	return nil
}

func (c *Controller) SyncRoute() {
	result, err := c.etcdCli.Get(context.TODO(), EtcdKeyNodeInfo, clientv3.WithPrefix())
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

	ctx, canF := context.WithCancel(context.Background())
	wc := c.etcdCli.Watch(ctx, EtcdKeyNodeInfo, clientv3.WithRev(result.Header.Revision))

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
			canF()
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
