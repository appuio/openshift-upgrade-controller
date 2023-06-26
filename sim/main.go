/* Interactive cluster version operator simulation
 *
 * Allows to toggle the cluster version update available and updating conditions.
 * Initializes required objects in the cluster. CRDs must be applied beforehand.
 */
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	configv1 "github.com/openshift/api/config/v1"
	machineconfigurationv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"go.uber.org/multierr"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/appuio/openshift-upgrade-controller/pkg/clusterversion"
	"github.com/appuio/openshift-upgrade-controller/pkg/healthcheck"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1.AddToScheme(scheme))
	utilruntime.Must(batchv1.AddToScheme(scheme))
	utilruntime.Must(machineconfigurationv1.AddToScheme(scheme))
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cl, err := client.New(config.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		fmt.Println("failed to create client")
		os.Exit(1)
	}

	fmt.Println("initializing objects...")
	if err := initObjects(cl, ctx); err != nil {
		fmt.Printf("failed to initialize objects: %s\n", err)
		os.Exit(1)
	}
	fmt.Println(asciiMoveUp + asciiClear + asciiMoveUp)

	ir := bufio.NewReader(os.Stdin)
	lines := make(chan string)
	go func() {
		for {
			line, _ := ir.ReadString('\n')
			lines <- strings.TrimSpace(line)
		}
	}()

	for {
		printStatusAndPrompt(cl, ctx)

		select {
		case <-ctx.Done():
			return
		case line := <-lines:
			switch line {
			case "v":
				fmt.Print(asciiMoveUp + asciiClear + "Updating...\n")
				err := toggleClusterVersionUpdating(ctx, cl)
				if err != nil {
					fmt.Println("failed to toggle cluster version: ", err)
					os.Exit(1)
				}
			case "a":
				fmt.Print(asciiMoveUp + asciiClear + "Updating...\n")
				err := toggleClusterVersionUpdateAvailable(ctx, cl)
				if err != nil {
					fmt.Println("failed to toggle available update: ", err)
					os.Exit(1)
				}
			case "p":
				fmt.Print(asciiMoveUp + asciiClear + "Updating...\n")
				togglePoolsUpdating(ctx, cl)
				if err != nil {
					fmt.Println("failed to toggle updating pools: ", err)
					os.Exit(1)
				}
			case "q":
				return
			}
		}

		resetPrompt()
	}
}

func initObjects(cl client.Client, ctx context.Context) error {
	cv := &configv1.ClusterVersion{}
	cv.Name = "version"
	_, err := controllerutil.CreateOrUpdate(ctx, cl, cv, func() error {
		if cv.Spec.DesiredUpdate == nil || cv.Spec.DesiredUpdate.Version == "" {
			cv.Spec.DesiredUpdate = &configv1.Update{
				Version: "4.11.42",
				Image:   fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%x", sha256.Sum256([]byte("4.11.42"))),
			}
		}
		return nil
	})

	mcpl := machineconfigurationv1.MachineConfigPoolList{}
	if err := cl.List(ctx, &mcpl); err != nil {
		return fmt.Errorf("failed to get MachineConfigPool: %w", err)
	}

	if len(mcpl.Items) == 0 {
		mcp := &machineconfigurationv1.MachineConfigPool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "master",
			},
		}
		if err := cl.Create(ctx, mcp); err != nil {
			return fmt.Errorf("failed to create MachineConfigPool: %w", err)
		}
	}

	return err
}

const (
	// https://gist.github.com/fnky/458719343aabd01cfb17a3a4f7296797
	asciiMoveUp  = "\033[F"
	asciiClear   = "\033[K"
	asciiReset   = "\033[0m"
	asciiPinkBG  = "\033[48;5;225m"
	asciiGreenBG = "\033[48;5;40m"
	asciiWhiteBG = "\033[48;5;15m"

	updatingMsg    = asciiPinkBG + "updating" + asciiReset
	updateAvailMsg = asciiGreenBG + "update_avail" + asciiReset
	msgIdle        = "idle"
)

func printStatusAndPrompt(cl client.Client, ctx context.Context) {
	cv := configv1.ClusterVersion{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "version"}, &cv); err != nil {
		fmt.Println("failed to get ClusterVersion")
		os.Exit(1)
	}

	mcpl := machineconfigurationv1.MachineConfigPoolList{}
	if err := cl.List(ctx, &mcpl); err != nil {
		fmt.Println("failed to get MachineConfigPool")
		os.Exit(1)
	}

	pools := healthcheck.MachineConfigPoolsUpdating(mcpl)
	poolsUpdating := len(pools) > 0
	cvUpdating := !clusterversion.IsVersionUpgradeCompleted(cv)
	cvHasUpdates := len(cv.Status.AvailableUpdates) > 0
	clusterUpdating := cvUpdating || poolsUpdating

	cvmsg := []string{msgIdle}
	if cvUpdating {
		cvmsg[0] = updatingMsg
	}
	if cvHasUpdates {
		cvmsg = append(cvmsg, updateAvailMsg)
	}
	if len(cvmsg) == 0 {
		cvmsg = append(cvmsg, msgIdle)
	}

	cmsg := msgIdle
	if clusterUpdating {
		cmsg = updatingMsg
	}

	pmsg := msgIdle
	if poolsUpdating {
		pmsg = updatingMsg
	}

	fmt.Printf("Cluster: %s, CV: %s, Pools: %s\n", cmsg, strings.Join(cvmsg, "+"), pmsg)
	fmt.Println("`v` = toggle CV updating, `a` = toggle update available, `p` = toggle pools upgrade, `q` = quit")
	fmt.Print("?> ")
}

func resetPrompt() {
	fmt.Print(asciiMoveUp + asciiClear)
	fmt.Print(asciiMoveUp + asciiClear)
	fmt.Print(asciiMoveUp + asciiClear)
}

func togglePoolsUpdating(ctx context.Context, cl client.Client) error {
	mcpl := machineconfigurationv1.MachineConfigPoolList{}
	if err := cl.List(ctx, &mcpl); err != nil {
		return fmt.Errorf("failed to get MachineConfigPool: %w", err)
	}

	if len(mcpl.Items) == 0 {
		return fmt.Errorf("no MachineConfigPools found")
	}

	updatingPools := make([]machineconfigurationv1.MachineConfigPool, 0, len(mcpl.Items))
	for _, mcp := range mcpl.Items {
		if mcp.Status.MachineCount == mcp.Status.UpdatedMachineCount {
			continue
		}
		updatingPools = append(updatingPools, mcp)
	}

	if len(updatingPools) == 0 {
		mcp := mcpl.Items[0]
		mcp.Status.MachineCount = 3
		mcp.Status.UpdatedMachineCount = 1
		if err := cl.Status().Update(ctx, &mcp); err != nil {
			return fmt.Errorf("failed to update MachineConfigPool: %w", err)
		}
	}

	var errs []error
	for _, mcp := range updatingPools {
		mcp.Status.MachineCount = mcp.Status.UpdatedMachineCount
		if err := cl.Status().Update(ctx, &mcp); err != nil {
			errs = append(errs, fmt.Errorf("failed to update MachineConfigPool: %w", err))
		}
	}

	return multierr.Combine(errs...)
}

func toggleClusterVersionUpdating(ctx context.Context, cl client.Client) error {
	cv := configv1.ClusterVersion{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "version"}, &cv); err != nil {
		fmt.Println("failed to get ClusterVersion")
		os.Exit(1)
	}

	if clusterversion.IsVersionUpgradeCompleted(cv) {
		if len(cv.Status.AvailableUpdates) == 0 {
			v, err := semver.NewVersion(cv.Spec.DesiredUpdate.Version)
			if err != nil {
				return fmt.Errorf("failed to parse DesiredUpdate.Version: %w", err)
			}
			dv := v.IncPatch().String()
			cv.Spec.DesiredUpdate.Version = dv
			cv.Spec.DesiredUpdate.Image = fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%x", sha256.Sum256([]byte(dv)))
			return cl.Update(ctx, &cv)
		}

		cv.Spec.DesiredUpdate.Version = cv.Status.AvailableUpdates[0].Version
		cv.Spec.DesiredUpdate.Image = cv.Status.AvailableUpdates[0].Image
		err := cl.Update(ctx, &cv)
		cv.Status.AvailableUpdates = cv.Status.AvailableUpdates[1:]
		return multierr.Combine(err, cl.Status().Update(ctx, &cv))
	}

	cv.Status.History = append(cv.Status.History, configv1.UpdateHistory{
		State:       configv1.CompletedUpdate,
		Version:     cv.Spec.DesiredUpdate.Version,
		Image:       cv.Spec.DesiredUpdate.Image,
		StartedTime: metav1.NewTime(time.Now().Add(-time.Hour)),
	})
	return cl.Status().Update(ctx, &cv)
}

func toggleClusterVersionUpdateAvailable(ctx context.Context, cl client.Client) error {
	cv := configv1.ClusterVersion{}
	if err := cl.Get(ctx, client.ObjectKey{Name: "version"}, &cv); err != nil {
		fmt.Println("failed to get ClusterVersion")
		os.Exit(1)
	}

	if len(cv.Status.AvailableUpdates) > 0 {
		cv.Status.AvailableUpdates = []configv1.Release{}
		return cl.Status().Update(ctx, &cv)
	}

	v, err := semver.NewVersion(cv.Spec.DesiredUpdate.Version)
	if err != nil {
		return fmt.Errorf("failed to parse DesiredUpdate.Version: %w", err)
	}
	dv := v.IncPatch().String()
	cv.Spec.DesiredUpdate.Version = dv
	cv.Spec.DesiredUpdate.Image = fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%x", sha256.Sum256([]byte(dv)))
	cv.Status.AvailableUpdates = []configv1.Release{
		{
			Version: dv,
			Image:   cv.Spec.DesiredUpdate.Image,
			URL:     "https://ocp.example.com",
			Channels: []string{
				fmt.Sprintf("stable-4.%d", v.Minor()),
			},
		},
	}

	return cl.Status().Update(ctx, &cv)
}
