package toimp

import configv1 "github.com/openshift/api/config/v1"

type ClusterID string

type LocalSpec struct {
	ClusterID ClusterID `json:"clusterID"`
}

func Convert(ls LocalSpec) configv1.ClusterVersionSpec {
	cvs := configv1.ClusterVersionSpec{}
	cvs.ClusterID = configv1.ClusterID(ls.ClusterID)
	return cvs
}
