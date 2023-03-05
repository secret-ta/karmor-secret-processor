package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	pb "karmor-secret-processor/protobuf"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/labstack/gommon/log"
	"google.golang.org/grpc"
)

var (
	port = flag.Int("port", 50051, "The server port")
	// apiserver = flag.String("apiserver", "http://localhost:8001", "The server port")
)

// key - val, podId - policyStatus
var policyStatusMapper = make(map[string]bool)
var mutex = &sync.RWMutex{}

type server struct {
	pb.UnimplementedProcessorServer
}

// type spec struct {
// 	Name      string `json:"name"`
// 	Namespace string `json:"namespace"`
// }

// type item struct {
// 	Spec spec `json:"spec"`
// }

// type karmorpolicies struct {
// 	Items []item `json:"items"`
// }

// func getAllPolicies() (*karmorpolicies, error) {
// 	var client = &http.Client{}
// 	var data *karmorpolicies = &karmorpolicies{}
// 	req, err := http.NewRequest("GET", *apiserver+"/apis/security.kubearmor.com/v1/kubearmorpolicies", nil)

// 	if err != nil {
// 		return nil, err
// 	}

// 	res, err := client.Do(req)
// 	if err != nil {
// 		return nil, err
// 	}
// 	defer res.Body.Close()

// 	err = json.NewDecoder(res.Body).Decode(data)
// 	if err != nil {
// 		return nil, err
// 	}

// 	return data, nil
// }

// func isPolicyHasCreated(policyName string) bool {
// 	policies, _ := getAllPolicies()
// 	for _, policy := range policies.Items {
// 		if strings.Contains(policy.Metadata, policyName) {
// 			return true
// 		}
// 	}
// 	return false
// }

func isPolicyHasCreated(deploymentName string) bool {
	mutex.Lock()
	defer mutex.Unlock()

	val, ok := policyStatusMapper[deploymentName]

	return ok && val
}

func processCreate(req *pb.Request) {

	keyValLabels := ""
	for key, val := range req.Labels {
		keyValLabels += "      " + key + ": " + val + "\n"
	}
	dataPolicy := map[string]string{
		"PolicyName":      fmt.Sprintf("%s-disable-secret-access", req.DeploymentName),
		"KeyValLabel":     keyValLabels,
		"SecretMountPath": fmt.Sprintf("%s/", req.SecretPath),
	}

	log.Print(dataPolicy)
	outPolicy, err := generateKarmorPolicy("./template/k8s-secret-karmor.yaml", dataPolicy)

	if err != nil {
		log.Fatal(err)
	}

	fileNameOut := "./generated/" + dataPolicy["PolicyName"] + ".yaml"
	ioutil.WriteFile(fileNameOut, outPolicy, 0644)

	cmd := exec.Command("kubectl", "apply", "-f", fileNameOut)
	out, err := cmd.Output()
	log.Print(out)

	if err != nil {
		mutex.Lock()
		policyStatusMapper[req.DeploymentName] = true
		mutex.Unlock()
	}
}

func generateKarmorPolicy(filePath string, availableData map[string]string) ([]byte, error) {
	tmpl, err := template.New(filepath.Base(filePath)).ParseFiles(filePath)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, availableData); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func processDelete(req *pb.Request) {
	fileName := "./generated/" + req.DeploymentName + "-disable-secret-access" + ".yaml"

	cmd := exec.Command("kubectl", "delete", "-f", fileName)
	out, err := cmd.Output()
	log.Print(out)
	if err == nil {
		err := os.Remove(fileName)
		if err != nil {
			log.Error(err)
		} else {
			mutex.Lock()
			delete(policyStatusMapper, req.DeploymentName)
			mutex.Unlock()
		}
	}
}

func processRequest(req *pb.Request) {
	log.Print(req.Action == "CREATE")
	if (req.Action == "CREATE") && (!isPolicyHasCreated(req.DeploymentName)) {
		processCreate(req)
	} else if req.Action == "DELETE" {
		processDelete(req)
	}
}

func (s *server) Process(ctx context.Context, in *pb.Request) (*pb.Response, error) {
	log.Printf("Received: %v", in.GetNamespace())
	log.Printf("Received: %v", in.GetDeploymentName())
	log.Printf("Received: %v", in.GetSecretPath())

	go processRequest(in)
	return &pb.Response{}, nil
}

// func getKubernetesClient() kubernetes.Interface {
// 	// construct the path to resolve to `~/.kube/config`
// 	kubeConfigPath := os.Getenv("HOME") + "/.kube/config"

// 	// create the config from the path
// 	config, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)
// 	if err != nil {
// 		log.Fatalf("getClusterConfig: %v", err)
// 	}

// 	// generate the client based off of the config
// 	client, err := kubernetes.NewForConfig(config)
// 	if err != nil {
// 		log.Fatalf("getClusterConfig: %v", err)
// 	}

// 	log.Info("Successfully constructed k8s client")
// 	return client
// }

func main() {
	flag.Parse()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	s := grpc.NewServer()
	pb.RegisterProcessorServer(s, &server{})
	log.Printf("server listening at %v", lis.Addr())
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
