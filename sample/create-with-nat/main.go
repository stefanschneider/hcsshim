// hcs-toy1 project main.go
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"github.com/Microsoft/hcsshim"
	"github.com/docker/docker/pkg/stringid"
)

// filterDriver is an HCSShim driver type for the Windows Filter driver.
const filterDriver = 1

var homeDir = "C:\\ProgramData\\docker\\windowsfilter"

// Ref: https://github.com/docker/docker/blob/34cc19f6702c23b2ae4aad2b169ca64154404f9f/daemon/graphdriver/windows/windows.go#L752-L768
func GetLayerChain(layerPath string) ([]string, error) {
	jPath := filepath.Join(layerPath, "layerchain.json")
	content, err := ioutil.ReadFile(jPath)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("Unable to read layerchain file - %s", err)
	}

	var layerChain []string
	err = json.Unmarshal(content, &layerChain)
	if err != nil {
		return nil, fmt.Errorf("Failed to unmarshall layerchain json - %s", err)
	}

	layerChain = append([]string{layerPath}, layerChain...)

	return layerChain, nil
}

// Returns: LayerFolderPaht, VolumePath
func CreateAndActivateContainerLayer(di hcsshim.DriverInfo, containerLayerId, parentLayerPath string) (string, string, error) {
	var err error

	parentLayerId := GetLayerId(parentLayerPath)
	log.Printf("Parent layer %v path has Id %v", parentLayerPath, parentLayerId)

	layerChain, err := GetLayerChain(parentLayerPath)
	if err != nil {
		return "", "", err
	}

	log.Printf("Layer chain %v path has Id %v", layerChain, parentLayerId)

	err = hcsshim.CreateSandboxLayer(di, containerLayerId, layerChain[0], layerChain)
	if err != nil {
		return "", "", err
	}

	err = hcsshim.ActivateLayer(di, containerLayerId)
	if err != nil {
		return "", "", err
	}

	err = hcsshim.PrepareLayer(di, containerLayerId, layerChain)
	if err != nil {
		return "", "", err
	}

	volumeMountPath, err := hcsshim.GetLayerMountPath(di, containerLayerId)
	if err != nil {
		return "", "", err
	}
	log.Printf("Container layer volume path %v", volumeMountPath)

	return GetLayerPath(di, containerLayerId), volumeMountPath, nil
}

func GetLayerId(layerPath string) string {
	return filepath.Base(layerPath)
}

func GetLayerPath(di hcsshim.DriverInfo, layerId string) string {
	return filepath.Join(di.HomeDir, layerId)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Print(`
This sample create a new container runs ping and then destroys the container.
		
Usage:
  sample.exe <base container Id>

To get the base container id for "microsoft/windowsservercore" use the following PS snippet:
  Split-Path -Leaf (docker inspect microsoft/windowsservercore  | ConvertFrom-Json).GraphDriver.Data.Dir

`)
		os.Exit(1)
	}

	windowsbaseId := os.Args[1]

	di := hcsshim.DriverInfo{
		HomeDir: homeDir,
		Flavour: filterDriver,
	}

	imgData, err := hcsshim.GetSharedBaseImages()
	panicIf(err)
	fmt.Println(imgData)

	hcsNets, err := hcsshim.HNSListNetworkRequest("GET", "", "")
	panicIf(err)
	fmt.Println(hcsNets)

	virtualNetworkId := ""
	for _, n := range hcsNets {
		if n.Name == "nat" {
			virtualNetworkId = n.Id
		}
	}

	// https://github.com/docker/libnetwork/blob/f9a1590164b878e668eabf889dd79fb6af8eaced/drivers/windows/windows.go#L284
	endpointRequest := hcsshim.HNSEndpoint{
		VirtualNetwork: virtualNetworkId,
	}
	endpointRequestJson, err := json.Marshal(endpointRequest)
	panicIf(err)

	endpoint, err := hcsshim.HNSEndpointRequest("POST", "", string(endpointRequestJson))
	panicIf(err)
	fmt.Println(*endpoint)

	windowsservercorePath, err := hcsshim.GetLayerMountPath(di, windowsbaseId)
	panicIf(err)
	fmt.Println(windowsservercorePath)

	layerChain, err := GetLayerChain(windowsservercorePath)
	panicIf(err)
	fmt.Println(layerChain)

	newContainerId := stringid.GenerateNonCryptoID()

	layerFolderPath, volumeMountPath, err := CreateAndActivateContainerLayer(di, newContainerId, windowsservercorePath)
	panicIf(err)

	containerConfig := hcsshim.ContainerConfig{
		SystemType:              "Container",
		Name:                    newContainerId,
		Owner:                   "Garden",
		LayerFolderPath:         layerFolderPath,
		VolumePath:              volumeMountPath,
		IgnoreFlushesDuringBoot: true,
		EndpointList:            []string{endpoint.Id},
	}

	// https://github.com/docker/docker/blob/cf58eb437c4229e876f2d952a228b603a074e584/libcontainerd/client_windows.go#L111-L121
	for _, layerPath := range layerChain {
		id, err := hcsshim.NameToGuid(GetLayerId(layerPath))
		panicIf(err)

		containerConfig.Layers = append(containerConfig.Layers, hcsshim.Layer{
			Path: layerPath,
			ID:   id.ToString(),
		})
	}

	c, err := hcsshim.CreateContainer(newContainerId, &containerConfig)
	panicIf(err)
	fmt.Println(c)

	err = c.Start()
	panicIf(err)

	stats, err := c.Statistics()
	panicIf(err)
	fmt.Println(stats)

	processConfig := hcsshim.ProcessConfig{
		CommandLine:      "ping 127.0.0.1",
		WorkingDirectory: "C:\\",
		//CreateStdErrPipe: true,
		//CreateStdInPipe:  true,
		//CreateStdOutPipe: true,
	}

	p, err := c.CreateProcess(&processConfig)
	panicIf(err)
	fmt.Println(p)

	err = p.Wait()
	panicIf(err)

	err = c.Shutdown()
	warnIf(err)

	err = c.Terminate()
	warnIf(err)

	endpoint, err = hcsshim.HNSEndpointRequest("DELETE", endpoint.Id, "")
	warnIf(err)

	err = hcsshim.UnprepareLayer(di, newContainerId)
	warnIf(err)

	err = hcsshim.DeactivateLayer(di, newContainerId)
	warnIf(err)

	err = hcsshim.DestroyLayer(di, newContainerId)
	warnIf(err)
}

func panicIf(err error) {
	if err != nil {
		panic(err)
	}
}

func warnIf(err error) {
	if err != nil {
		fmt.Errorf("%v", err)
	}
}
