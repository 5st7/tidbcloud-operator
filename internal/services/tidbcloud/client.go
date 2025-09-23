package tidbcloud

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/icholy/digest"
	"github.com/tidbcloud/tidbcloud-cli/pkg/tidbcloud/v1beta1/dedicated"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// Client interface defines operations for TiDB Cloud API
type Client interface {
	// Cluster operations
	GetCluster(ctx context.Context, projectID, clusterID string) (*ClusterInfo, error)
	ScaleCluster(ctx context.Context, projectID, clusterID string, req *ScaleRequest) error
	SuspendCluster(ctx context.Context, projectID, clusterID string) error
	ResumeCluster(ctx context.Context, projectID, clusterID string) error
}

// Config holds configuration for TiDB Cloud client
type Config struct {
	// Base URL for TiDB Cloud API
	BaseURL string

	// API credentials
	PublicKey  string
	PrivateKey string

	// HTTP client timeout
	Timeout time.Duration

	// Rate limiting settings
	RequestsPerMinute int
}

// ClusterInfo represents TiDB Cloud cluster information
type ClusterInfo struct {
	ClusterID     string            `json:"clusterId"`
	Name          string            `json:"name"`
	Status        ClusterStatus     `json:"status"`
	TiDBNodes     NodeConfiguration `json:"tidbNodeSetting"`
	TiKVNodes     NodeConfiguration `json:"tikvNodeSetting"`
	TiFlashNodes  NodeConfiguration `json:"tiflashNodeSetting"`
	Region        string            `json:"region"`
	CloudProvider string            `json:"cloudProvider"`
}

// NodeConfiguration represents node configuration for a cluster
type NodeConfiguration struct {
	NodeQuantity int    `json:"nodeQuantity"`
	NodeSize     string `json:"nodeSize"`
	Storage      int    `json:"storage"`
}

// ScaleRequest represents a scaling request
type ScaleRequest struct {
	TiDBNodeSetting    *NodeConfiguration `json:"tidbNodeSetting,omitempty"`
	TiKVNodeSetting    *NodeConfiguration `json:"tikvNodeSetting,omitempty"`
	TiFlashNodeSetting *NodeConfiguration `json:"tiflashNodeSetting,omitempty"`
}

// ClusterStatus represents the status of a TiDB Cloud cluster
type ClusterStatus string

const (
	StatusCreating  ClusterStatus = "CREATING"
	StatusAvailable ClusterStatus = "AVAILABLE"
	StatusModifying ClusterStatus = "MODIFYING"
	StatusPaused    ClusterStatus = "PAUSED"
	StatusResuming  ClusterStatus = "RESUMING"
)

// HTTPClient implements the Client interface using the official TiDB Cloud SDK
type HTTPClient struct {
	client *dedicated.APIClient
	config *Config
}

// NewClient creates a new TiDB Cloud client using the official SDK
func NewClient(config *Config) (*HTTPClient, error) {
	log := logf.Log.WithName("tidbcloud.client")

	if config.PublicKey == "" || config.PrivateKey == "" {
		return nil, fmt.Errorf("API credentials are required")
	}

	if config.BaseURL == "" {
		config.BaseURL = "https://api.tidbcloud.com/api/v1beta1"
	}

	log.Info("Creating TiDB Cloud client",
		"baseURL", config.BaseURL,
		"publicKey", config.PublicKey,
		"privateKeyLength", len(config.PrivateKey),
		"timeout", config.Timeout,
		"requestsPerMinute", config.RequestsPerMinute)

	// Create official SDK configuration (use default server settings like debug program)
	sdkConfig := dedicated.NewConfiguration()

	// Don't override default server configuration - use the same as debug program
	log.Info("Using default SDK server configuration (same as debug program)")

	log.Info("Configuring digest authentication for TiDB Cloud API")

	// Use digest authentication (the only working method based on debug tests)
	transport := &digest.Transport{
		Username: config.PublicKey,
		Password: config.PrivateKey,
	}

	httpClient := &http.Client{
		Transport: transport,
	}

	// Set timeout if provided
	if config.Timeout > 0 {
		httpClient.Timeout = config.Timeout
	}

	sdkConfig.HTTPClient = httpClient

	// Set User-Agent like debug program
	sdkConfig.UserAgent = "tidbcloud-operator/v1.0.0"

	// Create API client
	apiClient := dedicated.NewAPIClient(sdkConfig)

	log.Info("TiDB Cloud client created successfully with digest authentication")

	return &HTTPClient{
		client: apiClient,
		config: config,
	}, nil
}

// GetCluster retrieves cluster information using the official SDK
func (c *HTTPClient) GetCluster(ctx context.Context, projectID, clusterID string) (*ClusterInfo, error) {
	log := logf.FromContext(ctx)
	log.Info("Making API call to TiDB Cloud",
		"method", "ClusterServiceListClusters",
		"projectID", projectID,
		"clusterID", clusterID,
		"baseURL", c.config.BaseURL,
		"publicKey", c.config.PublicKey,
		"authMethod", "digest")

	// Try using ListClusters with project filter to find the specific cluster
	request := c.client.ClusterServiceAPI.ClusterServiceListClusters(ctx).
		ProjectId(projectID).
		ClusterIds([]string{clusterID})

	listResp, resp, err := c.client.ClusterServiceAPI.ClusterServiceListClustersExecute(request)
	if err != nil {
		log.Error(err, "TiDB Cloud API call failed",
			"method", "ClusterServiceListClusters",
			"projectID", projectID,
			"clusterID", clusterID,
			"httpStatus", func() string {
				if resp != nil {
					return resp.Status
				}
				return "unknown"
			}())
		return nil, fmt.Errorf("failed to get cluster: %w", err)
	}
	defer resp.Body.Close()

	log.Info("TiDB Cloud API call successful",
		"method", "ClusterServiceListClusters",
		"httpStatus", resp.Status,
		"clustersFound", len(listResp.Clusters))

	// Check if cluster was found
	if listResp.Clusters == nil || len(listResp.Clusters) == 0 {
		log.Error(nil, "Cluster not found in API response",
			"projectID", projectID,
			"clusterID", clusterID,
			"clustersReturned", len(listResp.Clusters))
		return nil, fmt.Errorf("cluster %s not found in project %s", clusterID, projectID)
	}

	// Return the first (and should be only) cluster
	cluster := &listResp.Clusters[0]
	log.Info("Cluster found successfully",
		"clusterId", cluster.GetClusterId(),
		"clusterName", cluster.GetDisplayName(),
		"clusterState", cluster.GetState())

	// Convert official SDK response to our internal format
	return convertToClusterInfo(cluster), nil
}

// ScaleCluster scales a TiDB Cloud cluster using the official SDK
func (c *HTTPClient) ScaleCluster(ctx context.Context, projectID, clusterID string, req *ScaleRequest) error {
	log := logf.FromContext(ctx)
	log.Info("Scaling TiDB Cloud cluster",
		"projectID", projectID,
		"clusterID", clusterID,
		"tidbNodes", func() int {
			if req.TiDBNodeSetting != nil {
				return req.TiDBNodeSetting.NodeQuantity
			}
			return 0
		}(),
		"tikvNodes", func() int {
			if req.TiKVNodeSetting != nil {
				return req.TiKVNodeSetting.NodeQuantity
			}
			return 0
		}(),
		"tiflashNodes", func() int {
			if req.TiFlashNodeSetting != nil {
				return req.TiFlashNodeSetting.NodeQuantity
			}
			return 0
		}())

	// Create the cluster update request body using the correct type
	updateCluster := dedicated.NewTheUpdatedClusterConfigurationWithDefaults()

	// Configure TiDB nodes if specified
	if req.TiDBNodeSetting != nil && req.TiDBNodeSetting.NodeQuantity > 0 {
		tidbNodeGroup := &dedicated.UpdateClusterRequestTidbNodeSettingTidbNodeGroup{}
		tidbNodeGroup.SetNodeCount(int32(req.TiDBNodeSetting.NodeQuantity))

		if req.TiDBNodeSetting.NodeSize != "" {
			tidbNodeGroup.SetNodeSpecKey(req.TiDBNodeSetting.NodeSize)
		}

		tidbSetting := &dedicated.V1beta1UpdateClusterRequestTidbNodeSetting{
			TidbNodeGroups: []dedicated.UpdateClusterRequestTidbNodeSettingTidbNodeGroup{*tidbNodeGroup},
		}
		updateCluster.SetTidbNodeSetting(*tidbSetting)
		log.Info("TiDB scaling configuration",
			"nodeQuantity", req.TiDBNodeSetting.NodeQuantity,
			"nodeSize", req.TiDBNodeSetting.NodeSize)
	}

	// Configure TiKV nodes if specified
	if req.TiKVNodeSetting != nil && req.TiKVNodeSetting.NodeQuantity > 0 {
		tikvSetting := &dedicated.V1beta1UpdateClusterRequestStorageNodeSetting{}
		tikvSetting.SetNodeCount(int32(req.TiKVNodeSetting.NodeQuantity))

		if req.TiKVNodeSetting.NodeSize != "" {
			tikvSetting.SetNodeSpecKey(req.TiKVNodeSetting.NodeSize)
		}
		if req.TiKVNodeSetting.Storage > 0 {
			tikvSetting.SetStorageSizeGi(int32(req.TiKVNodeSetting.Storage))
		}
		updateCluster.SetTikvNodeSetting(*tikvSetting)
		log.Info("TiKV scaling configuration",
			"nodeQuantity", req.TiKVNodeSetting.NodeQuantity,
			"nodeSize", req.TiKVNodeSetting.NodeSize,
			"storage", req.TiKVNodeSetting.Storage)
	}

	// Configure TiFlash nodes if specified
	if req.TiFlashNodeSetting != nil && req.TiFlashNodeSetting.NodeQuantity > 0 {
		tiflashSetting := &dedicated.V1beta1UpdateClusterRequestStorageNodeSetting{}
		tiflashSetting.SetNodeCount(int32(req.TiFlashNodeSetting.NodeQuantity))

		if req.TiFlashNodeSetting.NodeSize != "" {
			tiflashSetting.SetNodeSpecKey(req.TiFlashNodeSetting.NodeSize)
		}
		if req.TiFlashNodeSetting.Storage > 0 {
			tiflashSetting.SetStorageSizeGi(int32(req.TiFlashNodeSetting.Storage))
		}
		updateCluster.SetTiflashNodeSetting(*tiflashSetting)
		log.Info("TiFlash scaling configuration",
			"nodeQuantity", req.TiFlashNodeSetting.NodeQuantity,
			"nodeSize", req.TiFlashNodeSetting.NodeSize,
			"storage", req.TiFlashNodeSetting.Storage)
	}

	log.Info("Making TiDB Cloud scale API call",
		"method", "ClusterServiceUpdateCluster",
		"clusterId", clusterID,
		"authMethod", "digest",
		"requestDetails", fmt.Sprintf("TiDB:%+v, TiKV:%+v, TiFlash:%+v",
			func() interface{} {
				if updateCluster.TidbNodeSetting != nil {
					return *updateCluster.TidbNodeSetting
				}
				return nil
			}(),
			func() interface{} {
				if updateCluster.TikvNodeSetting != nil {
					return *updateCluster.TikvNodeSetting
				}
				return nil
			}(),
			func() interface{} {
				if updateCluster.TiflashNodeSetting != nil {
					return *updateCluster.TiflashNodeSetting
				}
				return nil
			}()))

	// Call the update API with the cluster configuration
	request := c.client.ClusterServiceAPI.ClusterServiceUpdateCluster(ctx, clusterID).
		Cluster(*updateCluster)

	cluster, resp, err := c.client.ClusterServiceAPI.ClusterServiceUpdateClusterExecute(request)
	if err != nil {
		// Try to read response body for more details
		responseBody := ""
		if resp != nil && resp.Body != nil {
			if bodyBytes, readErr := io.ReadAll(resp.Body); readErr == nil {
				responseBody = string(bodyBytes)
			}
		}

		log.Error(err, "TiDB Cloud scale API call failed",
			"method", "ClusterServiceUpdateCluster",
			"clusterId", clusterID,
			"httpStatus", func() string {
				if resp != nil {
					return resp.Status
				}
				return "unknown"
			}(),
			"responseBody", responseBody)
		return fmt.Errorf("failed to scale cluster: %w", err)
	}
	defer resp.Body.Close()

	log.Info("TiDB Cloud scale API call successful",
		"method", "ClusterServiceUpdateCluster",
		"httpStatus", resp.Status,
		"clusterId", cluster.GetClusterId(),
		"clusterName", cluster.GetDisplayName(),
		"clusterState", cluster.GetState())

	log.Info("Scaling operation completed",
		"projectID", projectID,
		"clusterID", clusterID,
		"newClusterState", cluster.GetState())

	return nil
}

// SuspendCluster suspends a TiDB Cloud cluster using the official SDK
func (c *HTTPClient) SuspendCluster(ctx context.Context, projectID, clusterID string) error {
	request := c.client.ClusterServiceAPI.ClusterServicePauseCluster(ctx, clusterID)

	_, resp, err := c.client.ClusterServiceAPI.ClusterServicePauseClusterExecute(request)
	if err != nil {
		return fmt.Errorf("failed to suspend cluster: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

// ResumeCluster resumes a suspended TiDB Cloud cluster using the official SDK
func (c *HTTPClient) ResumeCluster(ctx context.Context, projectID, clusterID string) error {
	request := c.client.ClusterServiceAPI.ClusterServiceResumeCluster(ctx, clusterID)

	_, resp, err := c.client.ClusterServiceAPI.ClusterServiceResumeClusterExecute(request)
	if err != nil {
		return fmt.Errorf("failed to resume cluster: %w", err)
	}
	defer resp.Body.Close()

	return nil
}

// Helper functions for conversion between formats

func convertToClusterInfo(cluster *dedicated.TidbCloudOpenApidedicatedv1beta1Cluster) *ClusterInfo {
	if cluster == nil {
		return nil
	}

	info := &ClusterInfo{
		ClusterID: cluster.GetClusterId(),
		Name:      cluster.GetDisplayName(),
		Status:    convertClusterStatus(cluster.GetState()),
	}

	// TODO: Convert node configurations when SDK documentation is available
	// tidb := cluster.GetTidbNodeSetting()
	info.TiDBNodes = NodeConfiguration{
		NodeQuantity: 1, // Placeholder
	}

	// tikv := cluster.GetTikvNodeSetting()
	info.TiKVNodes = NodeConfiguration{
		NodeQuantity: 3, // Placeholder
		Storage:      500,
	}

	if cluster.HasTiflashNodeSetting() {
		// tiflash := cluster.GetTiflashNodeSetting()
		info.TiFlashNodes = NodeConfiguration{
			NodeQuantity: 0, // Placeholder
			Storage:      0,
		}
	}

	return info
}

func convertClusterStatus(state dedicated.Commonv1beta1ClusterState) ClusterStatus {
	switch state {
	case dedicated.COMMONV1BETA1CLUSTERSTATE_CREATING:
		return StatusCreating
	case dedicated.COMMONV1BETA1CLUSTERSTATE_ACTIVE:
		return StatusAvailable
	case dedicated.COMMONV1BETA1CLUSTERSTATE_MODIFYING:
		return StatusModifying
	case dedicated.COMMONV1BETA1CLUSTERSTATE_PAUSED:
		return StatusPaused
	case dedicated.COMMONV1BETA1CLUSTERSTATE_RESUMING:
		return StatusResuming
	default:
		return StatusAvailable // Default fallback
	}
}

// TODO: Implement proper conversion functions when SDK documentation is available
