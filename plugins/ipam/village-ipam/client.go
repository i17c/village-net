package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/containernetworking/cni/pkg/types/current"
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
	AssignIp(containerId string) (*current.Result, error)
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
	Type        string `json:"type,omitempty"`
	Range       string `json:"range,omitempty"`
	GateWay     string `json:"gateway,omitempty"`
	IsDefaultGW bool   `json:"is_default_gw,omitempty"`
}

func NewIPAMClient(conf types.NetConf) (Interface, error) {
	kubeClient, err := clients.NewKubeClient(conf.Cluster.Kubeconfig)
	if err != nil {
		return nil, err
	}
	return &client{kubeClient: kubeClient}, nil
}

func (i *client) AssignIp(containerId string) (*current.Result, error) {
	// 从 apiserver 中读取 cm
	cm, err := i.kubeClient.CoreV1().ConfigMaps(ipamConfigMapNamespace).Get(context.TODO(), ipamConfigMap, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get cm: %v", err)
	}
	log.WriteString(cm.String() + "\n")

	result := current.Result{}
	interfaces := ifConfigs{}
	if err := json.Unmarshal([]byte(cm.Data[ipamCMInterfaceKey]), &interfaces); err != nil {
		return nil, fmt.Errorf("failed to marshal interfaces: %v", err)
	}
	cnIpMap := map[string]string{}
	if _, ok := cm.Data[ipamCMContainerIpKey]; !ok {
		d, _ := json.Marshal(cnIpMap)
		cm.Data[ipamCMContainerIpKey] = string(d)
	}
	if err := json.Unmarshal([]byte(cm.Data[ipamCMContainerIpKey]), &cnIpMap); err != nil {
		return nil, fmt.Errorf("failed to marshal containerIps: %v", err)
	}
	for i, dev := range interfaces {
		curIf := current.Interface{}
		curIp := current.IPConfig{}
		curIp.Interface = &i
		curIf.Name = dev.Name
		// 从 cm 中找到可用的 ip
		ip, ipnet, err := net.ParseCIDR(dev.Range)
		if err != nil {
			return nil, err
		}

		log.WriteString("select ip \n")
		for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); inc(ip) {
			// 在 cm 中检查该 ip 是否被占用
			_, ok := cnIpMap[ip.String()]
			if !ok {
				curIp.Address = net.IPNet{IP: ip, Mask: ipnet.Mask}
				log.WriteString(fmt.Sprintf("ip: %v\n", ip))
				break
			}
		}
		version := "4"
		if curIp.Address.IP.To4() == nil {
			version = "6"
		}
		curIp.Version = version
		curIp.Gateway = net.ParseIP(dev.GateWay)
		result.IPs = append(result.IPs, &curIp)
		result.Interfaces = append(result.Interfaces, &curIf)

		cnIpMap[curIp.Address.IP.String()] = containerId
	}
	d, _ := json.Marshal(cnIpMap)
	cm.Data[ipamCMContainerIpKey] = string(d)

	// 将上一步找到的 ip 与 containerId 绑定，写入到 cm 中
	_, err = i.kubeClient.CoreV1().ConfigMaps(ipamConfigMapNamespace).Update(context.TODO(), cm, metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	// 返回 ip
	log.WriteString("result in ipam client:\n")
	if l, err := json.Marshal(result); err != nil {
		log.WriteString(err.Error())
	} else {
		log.Write(l)
		log.WriteString("\n")
	}
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
