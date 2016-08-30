// hcs-toy1 project main.go
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/Microsoft/hcsshim"
	"github.com/docker/docker/pkg/stringid"
)

// filterDriver is an HCSShim driver type for the Windows Filter driver.
const filterDriver = 1

var homeDir = "C:\\ProgramData\\docker\\windowsfilter"

// Returns: LayerFolderPaht, VolumePath
func CreateAndActivateContainerLayer(di hcsshim.DriverInfo, containerLayerId, parentLayerPath string) (string, string, error) {
	var err error

	parentLayerId := GetLayerId(parentLayerPath)
	log.Printf("Parent layer %v path has Id %v", parentLayerPath, parentLayerId)

	err = hcsshim.CreateSandboxLayer(di, containerLayerId, parentLayerPath, []string{parentLayerPath})
	if err != nil {
		return "", "", err
	}

	err = hcsshim.ActivateLayer(di, containerLayerId)
	if err != nil {
		return "", "", err
	}

	err = hcsshim.PrepareLayer(di, containerLayerId, []string{parentLayerPath})
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

func GetLayerPath2(di hcsshim.DriverInfo, layerId, parentLayerPath string) (string, error) {
	parentLayerId := GetLayerId(parentLayerPath)
	err := hcsshim.CreateLayer(di, layerId, parentLayerId)
	if err != nil {
		return "", err
	}

	err = hcsshim.ActivateLayer(di, layerId)
	if err != nil {
		return "", err
	}

	err = hcsshim.PrepareLayer(di, layerId, []string{parentLayerPath})
	if err != nil {
		return "", err
	}

	layerFolderPath, err := hcsshim.GetLayerMountPath(di, layerId)
	if err != nil {
		return "", err
	}
	log.Printf("Container layer folder path %v", layerFolderPath)

	err = hcsshim.UnprepareLayer(di, layerId)
	if err != nil {
		return "", err
	}

	err = hcsshim.DeactivateLayer(di, layerId)
	if err != nil {
		return "", err
	}

	err = hcsshim.DestroyLayer(di, layerId)
	if err != nil {
		return "", err
	}

	return layerFolderPath, nil
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

	guid, err := hcsshim.NameToGuid(windowsbaseId)
	panicIf(err)

	windowsbaseGuid := guid.ToString()

	di := hcsshim.DriverInfo{
		HomeDir: homeDir,
		Flavour: filterDriver,
	}

	imgData, err := hcsshim.GetSharedBaseImages()
	panicIf(err)
	fmt.Println(imgData)

	windowsservercorePath, err := hcsshim.GetLayerMountPath(di, windowsbaseId)
	panicIf(err)
	fmt.Println(windowsservercorePath)

	newContainerId := stringid.GenerateNonCryptoID()

	layerFolderPath, volumeMountPath, err := CreateAndActivateContainerLayer(di, newContainerId, windowsservercorePath)
	panicIf(err)

	containerConfig := hcsshim.ContainerConfig{
		SystemType:      "Container",
		Name:            newContainerId,
		Owner:           "Garden",
		LayerFolderPath: layerFolderPath,
		VolumePath:      volumeMountPath,
		Layers: []hcsshim.Layer{
			hcsshim.Layer{Path: windowsservercorePath, ID: windowsbaseGuid},
		},
		IgnoreFlushesDuringBoot: true,
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
