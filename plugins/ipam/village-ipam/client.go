package main

import (
	"context"
	"encoding/json"
	"github.com/containernetworking/plugins/pkg/clients"
	"github.com/containernetworking/plugins/pkg/types"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"net"
)

const ipamConfigMap = "village-conf"
const ipamConfigMapNamespace = "default"
const ipamCMInterfaceKey = "interfaces"
const ipamCMContainerIpKey = "containerIps"

type Interface interface {
	AssignIp(containerId string) (*types.Result, error)
	DeleteIp(containerId string) error
	Check(containerId string) (bool, error)
}

type client struct {
	kubeClient *kubernetes.Clientset
	cm         *v1.ConfigMap
}

type ifConfigs []ifConfig

type ifConfig struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Range       string `json:"range"`
	GateWay     string `json:"gateway"`
	IsDefaultGW bool   `json:"is_default_gw"`
}

func NewIPAMClient(conf types.NetConf) (Interface, error) {
	kubeClient, err := clients.NewKubeClient(conf.Kubernetes.Kubeconfig)
	if err != nil {
		return nil, err
	}
	return &client{kubeClient: kubeClient}, nil
}

func (i *client) AssignIp(containerId string) (*types.Result, error) {
	// 从 apiserver 中读取 cm
	cm, err := i.kubeClient.CoreV1().ConfigMaps(ipamConfigMapNamespace).Get(context.TODO(), ipamConfigMap, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	result := types.Result{}
	interfaces := ifConfigs{}
	if err := json.Unmarshal([]byte(cm.Data[ipamCMInterfaceKey]), &interfaces); err != nil {
		return nil, err
	}
	cnIpMap := map[string]string{}
	if err := json.Unmarshal([]byte(cm.Data[ipamCMContainerIpKey]), &cnIpMap); err != nil {
		return nil, err
	}
	for _, dev := range interfaces {
		curIf := types.Interface{}
		curIp := types.IPConfig{}
		curIf.IsDefaultGW = dev.IsDefaultGW
		curIf.Type = dev.Type
		curIf.Name = dev.Name
		// 从 cm 中找到可用的 ip
		ip, ipnet, err := net.ParseCIDR(dev.Range)
		if err != nil {
			return nil, err
		}

		for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
			// 在 cm 中检查该 ip 是否被占用
			_, ok := cnIpMap[ip.String()]
			if !ok {
				curIp.Address = net.IPNet{IP: ip, Mask: ipnet.Mask}
				break
			}
		}
		version := "4"
		if curIp.Address.IP.To4() == nil {
			version = "6"
		}
		curIp.Version = version
		curIp.Gateway = net.ParseIP(dev.GateWay)
		curIf.IPs = append(curIf.IPs, &curIp)
		result.IPs = append(result.IPs, &curIp)
		result.Interfaces = append(result.Interfaces)

		cnIpMap[curIp.Address.IP.String()] = containerId
	}

	// 将上一步找到的 ip 与 containerId 绑定，写入到 cm 中
	_, err = i.kubeClient.CoreV1().ConfigMaps(ipamConfigMapNamespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	// 返回 ip
	return &result, nil
}

func inc(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func (i *client) Check(containerId string) (bool, error) {
	// 从 apiserver 中读取 cm
	cm, err := i.kubeClient.CoreV1().ConfigMaps(ipamConfigMapNamespace).Get(context.TODO(), ipamConfigMap, metav1.GetOptions{})
	if err != nil {
		return false, err
	}

	cnIpMap := map[string]string{}
	if err := json.Unmarshal([]byte(cm.Data[ipamCMContainerIpKey]), &cnIpMap); err != nil {
		return false, err
	}
	for _, value := range cnIpMap {
		if value == containerId {
			return true, err
		}
	}
	return false, nil
}

func (i *client) DeleteIp(containerId string) error {
	// 从 apiserver 中读取 cm
	cm, err := i.kubeClient.CoreV1().ConfigMaps(ipamConfigMapNamespace).Get(context.TODO(), ipamConfigMap, metav1.GetOptions{})
	if err != nil {
		return err
	}

	cnIpMap := map[string]string{}
	if err := json.Unmarshal([]byte(cm.Data[ipamCMContainerIpKey]), &cnIpMap); err != nil {
		return err
	}
	newCnIpMap := map[string]string{}
	for key, value := range cnIpMap {
		if value != containerId {
			newCnIpMap[key] = value
		}
	}
	if newCnIp, err := json.Marshal(newCnIpMap); err == nil {
		cm.Data[ipamCMContainerIpKey] = string(newCnIp)
	}

	_, err = i.kubeClient.CoreV1().ConfigMaps(ipamConfigMapNamespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	return nil
}
