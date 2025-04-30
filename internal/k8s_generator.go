package internal

import (
    "fmt"
    "os"
    "path/filepath"
    
    "gopkg.in/yaml.v2"
)

// K8sGenerator handles creation of Kubernetes manifests
type K8sGenerator struct {
    Manifest    *Manifest
    RecipeName  string
    StorageType string
    StoragePath string
    StorageClass string
    StorageSize  string
    NetworkName  string
    OutputDir    string
}

// NewK8sGenerator creates a new Kubernetes manifest generator
func NewK8sGenerator(manifest *Manifest, recipeName string, outputDir string) *K8sGenerator {
    return &K8sGenerator{
        Manifest:    manifest,
        RecipeName:  recipeName,
        StorageType: "local-path",
        StoragePath: "/data/builder-playground",
        StorageClass: "standard",
        StorageSize:  "10Gi",
        OutputDir:    outputDir,
    }
}

// Generate creates a Kubernetes manifest and writes it to disk
func (g *K8sGenerator) Generate() error {
    // Generate the CRD
    crd, err := g.buildCRD()
    if err != nil {
        return fmt.Errorf("failed to build CRD: %w", err)
    }
    
    // Marshal to YAML
    yamlData, err := yaml.Marshal(crd)
    if err != nil {
        return fmt.Errorf("failed to marshal CRD to YAML: %w", err)
    }
    
    // Write to file
    outputPath := filepath.Join(g.OutputDir, "k8s-manifest.yaml")
    if err := os.WriteFile(outputPath, yamlData, 0644); err != nil {
        return fmt.Errorf("failed to write manifest to %s: %w", outputPath, err)
    }
    
    return nil
}

// BuilderPlaygroundDeployment represents the top-level K8s CRD
type BuilderPlaygroundDeployment struct {
    // APIVersion is the Kubernetes API version for this resource
    APIVersion string `yaml:"apiVersion"`
    // Kind identifies this as a BuilderPlaygroundDeployment
    Kind string `yaml:"kind"`
    // Metadata contains the resource metadata
    Metadata BuilderPlaygroundMetadata `yaml:"metadata"`
    // Spec defines the desired state of the deployment
    Spec BuilderPlaygroundSpec `yaml:"spec"`
}

// BuilderPlaygroundMetadata contains the resource metadata
type BuilderPlaygroundMetadata struct {
    // Name is the name of the deployment
    Name string `yaml:"name"`
}

// BuilderPlaygroundSpec defines the desired state of the deployment
type BuilderPlaygroundSpec struct {
    // Recipe is the builder-playground recipe used (l1, opstack, etc)
    Recipe string `yaml:"recipe"`
    // Storage defines how persistent data should be stored
    Storage BuilderPlaygroundStorage `yaml:"storage"`
    // Network defines networking configuration (optional)
    Network *BuilderPlaygroundNetwork `yaml:"network,omitempty"`
    // Services is the list of services in this deployment
    Services []BuilderPlaygroundService `yaml:"services"`
}

// BuilderPlaygroundStorage defines storage configuration
type BuilderPlaygroundStorage struct {
    // Type is the storage type, either "local-path" or "pvc"
    Type string `yaml:"type"`
    // Path is the host path for local-path storage (used when type is "local-path")
    Path string `yaml:"path,omitempty"`
    // StorageClass is the K8s storage class (used when type is "pvc")
    StorageClass string `yaml:"storageClass,omitempty"`
    // Size is the storage size (used when type is "pvc")
    Size string `yaml:"size,omitempty"`
}

// BuilderPlaygroundNetwork defines network configuration
type BuilderPlaygroundNetwork struct {
    // Name is the name of the network
    Name string `yaml:"name"`
}

// BuilderPlaygroundService represents a single service in the deployment
type BuilderPlaygroundService struct {
    // Name is the service name
    Name string `yaml:"name"`
    // Image is the container image
    Image string `yaml:"image"`
    // Tag is the container image tag
    Tag string `yaml:"tag"`
    // Entrypoint overrides the container entrypoint
    Entrypoint []string `yaml:"entrypoint,omitempty"`
    // Args are the container command arguments
    Args []string `yaml:"args,omitempty"`
    // Env defines environment variables
    Env map[string]string `yaml:"env,omitempty"`
    // Ports are the container ports to expose
    Ports []BuilderPlaygroundPort `yaml:"ports,omitempty"`
    // Dependencies defines services this service depends on
    Dependencies []BuilderPlaygroundDependency `yaml:"dependencies,omitempty"`
    // ReadyCheck defines how to determine service readiness
    ReadyCheck *BuilderPlaygroundReadyCheck `yaml:"readyCheck,omitempty"`
    // Labels are the service labels
    Labels map[string]string `yaml:"labels,omitempty"`
    // UseHostExecution indicates whether to run on host instead of in container
    UseHostExecution bool `yaml:"useHostExecution,omitempty"`
    // Volumes are the volume mounts for the service
    Volumes []BuilderPlaygroundVolume `yaml:"volumes,omitempty"`
}

// BuilderPlaygroundPort represents a port configuration
type BuilderPlaygroundPort struct {
    // Name is a unique identifier for this port
    Name string `yaml:"name"`
    // Port is the container port number
    Port int `yaml:"port"`
    // Protocol is either "tcp" or "udp"
    Protocol string `yaml:"protocol,omitempty"`
    // HostPort is the port to expose on the host (if applicable)
    HostPort int `yaml:"hostPort,omitempty"`
}

// BuilderPlaygroundDependency represents a service dependency
type BuilderPlaygroundDependency struct {
    // Name is the name of the dependent service
    Name string `yaml:"name"`
    // Condition is either "running" or "healthy"
    Condition string `yaml:"condition"`
}

// BuilderPlaygroundReadyCheck defines readiness checking
type BuilderPlaygroundReadyCheck struct {
    // QueryURL is the URL to query for readiness
    QueryURL string `yaml:"queryURL,omitempty"`
    // Test is the command to run for readiness check
    Test []string `yaml:"test,omitempty"`
    // Interval is the time between checks
    Interval string `yaml:"interval,omitempty"`
    // Timeout is the maximum time for a check
    Timeout string `yaml:"timeout,omitempty"`
    // Retries is the number of retry attempts
    Retries int `yaml:"retries,omitempty"`
    // StartPeriod is the initial delay before checks begin
    StartPeriod string `yaml:"startPeriod,omitempty"`
}

// BuilderPlaygroundVolume represents a volume mount
type BuilderPlaygroundVolume struct {
    // Name is the volume name
    Name string `yaml:"name"`
    // MountPath is the path in the container
    MountPath string `yaml:"mountPath"`
    // SubPath is the path within the volume (optional)
    SubPath string `yaml:"subPath,omitempty"`
}

// buildCRD creates the CRD structure
func (g *K8sGenerator) buildCRD() (BuilderPlaygroundDeployment, error) {
    crd := BuilderPlaygroundDeployment{
        APIVersion: "playground.flashbots.io/v1alpha1",
        Kind:       "BuilderPlaygroundDeployment",
        Metadata: BuilderPlaygroundMetadata{
            Name: "builder-playground-" + g.RecipeName,
        },
        Spec: BuilderPlaygroundSpec{
            Recipe: g.RecipeName,
            Storage: BuilderPlaygroundStorage{
                Type: g.StorageType,
            },
            Services: []BuilderPlaygroundService{},
        },
    }

    // Configure storage based on type
    if g.StorageType == "local-path" {
        crd.Spec.Storage.Path = g.StoragePath
    } else if g.StorageType == "pvc" {
        crd.Spec.Storage.StorageClass = g.StorageClass
        crd.Spec.Storage.Size = g.StorageSize
    }
    
    // Add network configuration if available
    if g.NetworkName != "" {
        crd.Spec.Network = &BuilderPlaygroundNetwork{
            Name: g.NetworkName,
        }
    }
    
    // Convert services
    for _, svc := range g.Manifest.Services() {
        k8sService, err := convertServiceToK8s(svc)
        if err != nil {
            return crd, fmt.Errorf("failed to convert service %s: %w", svc.Name, err)
        }
        crd.Spec.Services = append(crd.Spec.Services, k8sService)
    }
    
    return crd, nil
}

// Define internal labels that should be filtered out
var internalLabels = map[string]bool{
    "service":            true,
    "playground":         true,
    "playground.session": true,
}

// convertServiceToK8s converts a service to Kubernetes representation
func convertServiceToK8s(svc *service) (BuilderPlaygroundService, error) {
    // Validate required fields
    if svc.image == "" {
        return BuilderPlaygroundService{}, fmt.Errorf("service %s missing required image", svc.Name)
    }
    
    k8sService := BuilderPlaygroundService{
        Name:  svc.Name,
        Image: svc.image,
        Tag:   svc.tag,
    }
    
    // Convert entrypoint
    if svc.entrypoint != "" {
        k8sService.Entrypoint = []string{svc.entrypoint}
    }
    
    // Convert args
    if len(svc.args) > 0 {
        k8sService.Args = svc.args
    }
    
    // Convert env variables
    if len(svc.env) > 0 {
        k8sService.Env = make(map[string]string)
        for k, v := range svc.env {
            k8sService.Env[k] = v
        }
    }
    
    // Convert ports
    if len(svc.ports) > 0 {
        k8sPorts := make([]BuilderPlaygroundPort, 0, len(svc.ports))
        for _, port := range svc.ports {
            k8sPort := BuilderPlaygroundPort{
                Name:     port.Name,
                Port:     port.Port,
                Protocol: port.Protocol,
            }
            if port.HostPort != 0 {
                k8sPort.HostPort = port.HostPort
            }
            k8sPorts = append(k8sPorts, k8sPort)
        }
        k8sService.Ports = k8sPorts
    }
    
    // Convert dependencies
    if len(svc.dependsOn) > 0 {
        k8sDeps := make([]BuilderPlaygroundDependency, 0, len(svc.dependsOn))
        for _, dep := range svc.dependsOn {
            condition := "running"
            if dep.Condition == DependsOnConditionHealthy {
                condition = "healthy"
            }
            
            k8sDep := BuilderPlaygroundDependency{
                Name:      dep.Name,
                Condition: condition,
            }
            k8sDeps = append(k8sDeps, k8sDep)
        }
        k8sService.Dependencies = k8sDeps
    }
    
    // Convert readiness check
    if svc.readyCheck != nil {
        k8sReadyCheck := &BuilderPlaygroundReadyCheck{}
        
        if svc.readyCheck.QueryURL != "" {
            k8sReadyCheck.QueryURL = svc.readyCheck.QueryURL
        }
        
        if len(svc.readyCheck.Test) > 0 {
            k8sReadyCheck.Test = svc.readyCheck.Test
        }
        
        if svc.readyCheck.Interval != 0 {
            k8sReadyCheck.Interval = svc.readyCheck.Interval.String()
        }
        
        if svc.readyCheck.Timeout != 0 {
            k8sReadyCheck.Timeout = svc.readyCheck.Timeout.String()
        }
        
        if svc.readyCheck.Retries != 0 {
            k8sReadyCheck.Retries = svc.readyCheck.Retries
        }
        
        if svc.readyCheck.StartPeriod != 0 {
            k8sReadyCheck.StartPeriod = svc.readyCheck.StartPeriod.String()
        }
        
        // Only add the ready check if at least one field is set
        if k8sReadyCheck.QueryURL != "" || len(k8sReadyCheck.Test) > 0 {
            k8sService.ReadyCheck = k8sReadyCheck
        }
    }
    
    // Convert labels
    if len(svc.labels) > 0 {
        serviceLabels := make(map[string]string)
        
        for k, v := range svc.labels {
            // Skip internal labels
            if !internalLabels[k] {
                serviceLabels[k] = v
            }
        }
        
        // Check for use-host-execution
        if useHost, ok := svc.labels["use-host-execution"]; ok && useHost == "true" {
            k8sService.UseHostExecution = true
        }
        
        if len(serviceLabels) > 0 {
            k8sService.Labels = serviceLabels
        }
    }
    
    // Add standard artifacts volume
    k8sService.Volumes = []BuilderPlaygroundVolume{
        {
            Name:      "artifacts",
            MountPath: "/artifacts",
        },
    }
    
    return k8sService, nil
}

// GenerateK8sManifest creates a Kubernetes manifest from a builder-playground manifest
// This is the main entry point for integration with the codebase
func GenerateK8sManifest(manifest *Manifest, recipeName string, output *output, storageType string, storageParams map[string]string) error {
    // Get absolute output directory path
    outputDir, err := output.AbsoluteDstPath()
    if err != nil {
        return fmt.Errorf("failed to get absolute output directory path: %w", err)
    }
    
    generator := NewK8sGenerator(manifest, recipeName, outputDir)
    
    // Configure storage settings
    generator.StorageType = storageType
    
    if storageType == "local-path" {
        if path, ok := storageParams["path"]; ok {
            generator.StoragePath = path
        }
    } else if storageType == "pvc" {
        if class, ok := storageParams["class"]; ok {
            generator.StorageClass = class
        }
        if size, ok := storageParams["size"]; ok {
            generator.StorageSize = size
        }
    }
    
    // Set network name if provided
    if netName, ok := storageParams["network"]; ok {
        generator.NetworkName = netName
    }
    
    return generator.Generate()
}