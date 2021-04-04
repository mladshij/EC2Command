package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/route53"
)

var (
	myInstanceID string
	myDomainZone string
)

// GlobalsData contains some global options
type GlobalsData struct {
	InstanceID   string `json:"EC2InstanceID"`
	DomainZoneID string `json:"DomainZoneID"`
}

func initOptions(filename string) (result bool) {

	file, errFile := ioutil.ReadFile(filename)
	if errFile != nil {
		panic(errFile)
	}

	if !json.Valid(file) {
		panic("Error: invalid global options file")
	}

	var options GlobalsData

	errJson := json.Unmarshal(file, &options)
	if errJson != nil {
		panic(errJson)
	}

	myInstanceID = options.InstanceID
	myDomainZone = options.DomainZoneID

	return true
}

func checkForInstanceState(ec2handler *ec2.EC2, instanceID string) string {

	tmpAllIns := true
	inputMyEC2 := &ec2.DescribeInstanceStatusInput{
		Filters: []*ec2.Filter{
			{
				Name: aws.String("availability-zone"),
				Values: []*string{
					aws.String("us-east-2c"),
				},
			},
		},
		IncludeAllInstances: &tmpAllIns,
		InstanceIds: []*string{
			aws.String(instanceID),
		},
	}

	resStatusMyEC2, err := ec2handler.DescribeInstanceStatus(inputMyEC2)

	if err != nil {
		fmt.Println("Error on receiving status of instance")
		fmt.Println(err.Error())
		return "Error"
	}

	//fmt.Println(resStatusMyEC2)
	for _, val := range resStatusMyEC2.InstanceStatuses {
		if strings.Compare(*val.InstanceId, instanceID) == 0 {
			//fmt.Printf("Instance found, state = %s\n", *val.InstanceState.Name)
			return *val.InstanceState.Name
		}
	}

	return "Unknown state"
}

func getInstancePublicIP(ec2handler *ec2.EC2, instanceID string) (string, error) {

	inputMyEC2 := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{
			aws.String(myInstanceID),
		},
	}

	resMyInstance, _ := ec2handler.DescribeInstances(inputMyEC2)

	for _, valRes := range resMyInstance.Reservations {
		for _, valIns := range valRes.Instances {
			if strings.Compare(*valIns.InstanceId, instanceID) == 0 {
				return *valIns.PublicIpAddress, nil
			}
		}
	}

	return "Unknown", errors.New("Unknown IP address")
}

func main() {
	var needStart = false

	fileOptions := flag.String("ini", "EC2Commands.json", "json file name with options")
	cmdType := flag.String("cmd", "status", "command: start or stop")

	// Читаем настройки
	initOptions(*fileOptions)

	// New session
	sess := session.Must(session.NewSessionWithOptions(session.Options{
		Config: aws.Config{Region: aws.String("us-east-2")}}))

	fmt.Println("Session established.")

	svcEC2 := ec2.New(sess)

	currentInstanceState := checkForInstanceState(svcEC2, myInstanceID)

	fmt.Printf("Current state is %s\n", currentInstanceState)

	switch *cmdType {
	case "status":
		if strings.Compare(currentInstanceState, ec2.InstanceStateNameRunning) == 0 {
			currentIP, _ := getInstancePublicIP(svcEC2, myInstanceID)
			fmt.Printf("Instance is running, current IP = %s.\n", currentIP)
			return
		}
	case "start":

		if strings.Compare(currentInstanceState, ec2.InstanceStateNameRunning) == 0 {
			currentIP, _ := getInstancePublicIP(svcEC2, myInstanceID)
			fmt.Printf("Instance is running, current IP = %s. Program exited.\n", currentIP)
			return
		}

		needStart = strings.Compare(currentInstanceState, ec2.InstanceStateNameStopped) == 0

		if needStart {
			fmt.Println("Trying to start instance")
			svcEC2.StartInstances(&ec2.StartInstancesInput{
				InstanceIds: []*string{
					aws.String(myInstanceID),
				},
			})

			for currentInstanceState = checkForInstanceState(svcEC2, myInstanceID); currentInstanceState != ec2.InstanceStateNameRunning; currentInstanceState = checkForInstanceState(svcEC2, myInstanceID) {
				fmt.Printf("Current status = %s, waiting...\n", currentInstanceState)
				time.Sleep(5 * time.Second)
			}
		}
		fmt.Println("Instance has been started")

		publicIP, errIP := getInstancePublicIP(svcEC2, myInstanceID)

		if errIP == nil {
			fmt.Printf("Instance address = %s\n", publicIP)
		} else {
			fmt.Println(errIP.Error())
			return
		}

		svcRoute53 := route53.New(sess)

		inputDomains := &route53.ChangeResourceRecordSetsInput{
			ChangeBatch: &route53.ChangeBatch{
				Changes: []*route53.Change{
					{
						Action: aws.String("UPSERT"),
						ResourceRecordSet: &route53.ResourceRecordSet{
							Name: aws.String("proxy.vorotyntsev.name"),
							ResourceRecords: []*route53.ResourceRecord{
								{
									Value: aws.String(publicIP),
								},
							},
							TTL:  aws.Int64(900),
							Type: aws.String("A"),
						},
					},
				},
				Comment: aws.String("Test record"),
			},
			HostedZoneId: aws.String(myDomainZone),
		}

		_, err := svcRoute53.ChangeResourceRecordSets(inputDomains)
		if err != nil {
			if aerr, ok := err.(awserr.Error); ok {
				switch aerr.Code() {
				case route53.ErrCodeNoSuchHostedZone:
					fmt.Println(route53.ErrCodeNoSuchHostedZone, aerr.Error())
				case route53.ErrCodeNoSuchHealthCheck:
					fmt.Println(route53.ErrCodeNoSuchHealthCheck, aerr.Error())
				case route53.ErrCodeInvalidChangeBatch:
					fmt.Println(route53.ErrCodeInvalidChangeBatch, aerr.Error())
				case route53.ErrCodeInvalidInput:
					fmt.Println(route53.ErrCodeInvalidInput, aerr.Error())
				case route53.ErrCodePriorRequestNotComplete:
					fmt.Println(route53.ErrCodePriorRequestNotComplete, aerr.Error())
				default:
					fmt.Println(aerr.Error())
				}
			} else {
				// Print the error, cast err to awserr.Error to get the Code and
				// Message from an error.
				fmt.Println(err.Error())
			}
		} else {
			fmt.Printf("Record in domain updated to %s\n", publicIP)
		}
	case "stop":
		if strings.Compare(currentInstanceState, ec2.InstanceStateNameRunning) != 0 {
			fmt.Printf("Instance is not running. Program exited.\n")
			return
		}

		needStop := strings.Compare(currentInstanceState, ec2.InstanceStateNameRunning) == 0

		if needStop {
			fmt.Println("Trying to stop instance")
			svcEC2.StopInstances(&ec2.StopInstancesInput{
				InstanceIds: []*string{
					aws.String(myInstanceID),
				},
			})

			for currentInstanceState = checkForInstanceState(svcEC2, myInstanceID); currentInstanceState != ec2.InstanceStateNameStopped; currentInstanceState = checkForInstanceState(svcEC2, myInstanceID) {
				fmt.Printf("Current status = %s, waiting...\n", currentInstanceState)
				time.Sleep(5 * time.Second)
			}
		}
		fmt.Println("Instance has been stopped")
	}
}
