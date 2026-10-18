// Copyright 2026 3eAI Labs Ltd
// SPDX-License-Identifier: AGPL-3.0-only

package gx

import (
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
)

// spec29212 is TS 29.212 V17.3.0 Table 5.3.0.1, "Gx specific Diameter
// AVPs": name → code, clause, type and whether the M bit is set (the AVP
// Flag rules' "Must" column). Extracted from the ETSI text of the table
// (pdftotext -layout, lines 8102-8483); the table writes three M,V entries
// as "M.V". Every 3GPP AVP of this table carries the V bit.
var spec29212 = map[string]specAVP{
	"3GPP-PS-Data-Off-Status":               {2847, "5.3.133", "Enumerated", false},
	"AN-GW-Address":                         {1050, "5.3.49", "Address", false},
	"AN-GW-Status":                          {2811, "5.3.100", "Enumerated", false},
	"APN-Aggregate-Max-Bitrate-DL":          {1040, "5.3.39", "Unsigned32", false},
	"APN-Aggregate-Max-Bitrate-UL":          {1041, "5.3.40", "Unsigned32", false},
	"Access-Availability-Change-Reason":     {2833, "5.3.121", "Unsigned32", false},
	"Access-Network-Charging-Identifier-Gx": {1022, "5.3.22", "Grouped", true},
	"Allocation-Retention-Priority":         {1034, "5.3.32", "Grouped", false},
	"Application-Detection-Information":     {1098, "5.3.91", "Grouped", false},
	"Bearer-Control-Mode":                   {1023, "5.3.23", "Enumerated", true},
	"Bearer-Identifier":                     {1020, "5.3.20", "OctetString", true},
	"Bearer-Operation":                      {1021, "5.3.21", "Enumerated", true},
	"Bearer-Usage":                          {1000, "5.3.1", "Enumerated", true},
	"CSG-Information-Reporting":             {1071, "5.3.64", "Enumerated", false},
	"Charging-Correlation-Indicator":        {1073, "5.3.67", "Enumerated", false},
	"Charging-Rule-Base-Name":               {1004, "5.3.5", "UTF8String", true},
	"Charging-Rule-Definition":              {1003, "5.3.4", "Grouped", true},
	"Charging-Rule-Install":                 {1001, "5.3.2", "Grouped", true},
	"Charging-Rule-Name":                    {1005, "5.3.6", "OctetString", true},
	"Charging-Rule-Remove":                  {1002, "5.3.3", "Grouped", true},
	"Charging-Rule-Report":                  {1018, "5.3.18", "Grouped", true},
	"CoA-IP-Address":                        {1035, "5.3.33", "Address", false},
	"CoA-Information":                       {1039, "5.3.37", "Grouped", false},
	"Conditional-APN-Aggregate-Max-Bitrate": {2818, "5.3.105", "Grouped", false},
	"Conditional-Policy-Information":        {2840, "5.3.128", "Grouped", false},
	"Credit-Management-Status":              {1082, "5.3.102", "Unsigned32", false},
	"Default-Access":                        {2829, "5.3.120", "Enumerated", false},
	"Default-Bearer-Indication":             {2844, "5.3.132", "Enumerated", false},
	"Default-EPS-Bearer-QoS":                {1049, "5.3.48", "Grouped", false},
	"Default-QoS-Information":               {2816, "5.3.103", "Grouped", false},
	"Default-QoS-Name":                      {2817, "5.3.104", "UTF8String", false},
	"Event-Report-Indication":               {1033, "5.3.30", "Grouped", false},
	"Event-Trigger":                         {1006, "5.3.7", "Enumerated", true},
	"Execution-Time":                        {2839, "5.3.127", "Time", false},
	"Extended-APN-AMBR-DL":                  {2848, "5.3.134", "Unsigned32", false},
	"Extended-APN-AMBR-UL":                  {2849, "5.3.135", "Unsigned32", false},
	"Extended-GBR-DL":                       {2850, "5.3.136", "Unsigned32", false},
	"Extended-GBR-UL":                       {2851, "5.3.137", "Unsigned32", false},
	"Fixed-User-Location-Info":              {2825, "5.3.112", "Grouped", false},
	"Flow-Direction":                        {1080, "5.3.65", "Enumerated", false},
	"Flow-Information":                      {1058, "5.3.53", "Grouped", false},
	"Flow-Label":                            {1057, "5.3.52", "OctetString", false},
	"Guaranteed-Bitrate-DL":                 {1025, "5.3.25", "Unsigned32", true},
	"Guaranteed-Bitrate-UL":                 {1026, "5.3.26", "Unsigned32", true},
	"HeNB-Local-IP-Address":                 {2804, "5.3.95", "Address", false},
	"IP-CAN-Session-Charging-Scope":         {2827, "5.3.114", "Enumerated", false},
	"IP-CAN-Type":                           {1027, "5.3.27", "Enumerated", true},
	"Max-PLR-DL":                            {2852, "5.3.138", "Float32", false},
	"Max-PLR-UL":                            {2853, "5.3.139", "Float32", false},
	"Metering-Method":                       {1007, "5.3.8", "Enumerated", true},
	"Monitoring-Flags":                      {2828, "5.3.115", "Unsigned32", false},
	"Monitoring-Key":                        {1066, "5.3.59", "OctetString", false},
	"Monitoring-Time":                       {2810, "5.3.99", "Time", false},
	"Mute-Notification":                     {2809, "5.3.98", "Enumerated", false},
	"NBIFOM-Mode":                           {2830, "5.3.117", "Enumerated", false},
	"NBIFOM-Support":                        {2831, "5.3.116", "Enumerated", false},
	"NetLoc-Access-Support":                 {2824, "5.3.111", "Unsigned32", false},
	"Network-Request-Support":               {1024, "5.3.24", "Enumerated", true},
	"Offline":                               {1008, "5.3.9", "Enumerated", true},
	"Online":                                {1009, "5.3.10", "Enumerated", true},
	"PCC-Rule-Status":                       {1019, "5.3.19", "Enumerated", true},
	"PCSCF-Restoration-Indication":          {2826, "5.3.113", "Unsigned32", false},
	"PDN-Connection-ID":                     {1065, "5.3.58", "OctetString", false},
	"PRA-Install":                           {2845, "5.3.130", "Grouped", false},
	"PRA-Remove":                            {2846, "5.3.131", "Grouped", false},
	"PS-to-CS-Session-Continuity":           {1099, "5.3.84", "Enumerated", false},
	"Packet-Filter-Content":                 {1059, "5.3.54", "IPFilterRule", false},
	"Packet-Filter-Identifier":              {1060, "5.3.55", "OctetString", false},
	"Packet-Filter-Information":             {1061, "5.3.56", "Grouped", false},
	"Packet-Filter-Operation":               {1062, "5.3.57", "Enumerated", false},
	"Packet-Filter-Usage":                   {1072, "5.3.66", "Enumerated", false},
	"Pre-emption-Capability":                {1047, "5.3.46", "Enumerated", false},
	"Pre-emption-Vulnerability":             {1048, "5.3.47", "Enumerated", false},
	"Precedence":                            {1010, "5.3.11", "Unsigned32", true},
	"Presence-Reporting-Area-Elements-List": {2820, "5.3.107", "OctetString", false},
	"Presence-Reporting-Area-Identifier":    {2821, "5.3.108", "OctetString", false},
	"Presence-Reporting-Area-Information":   {2822, "5.3.109", "Grouped", false},
	"Presence-Reporting-Area-Node":          {2855, "5.3.141", "Unsigned32", false},
	"Presence-Reporting-Area-Status":        {2823, "5.3.110", "Unsigned32", false},
	"Priority-Level":                        {1046, "5.3.45", "Unsigned32", false},
	"QoS-Class-Identifier":                  {1028, "5.3.17", "Enumerated", true},
	"QoS-Information":                       {1016, "5.3.16", "Grouped", true},
	"QoS-Negotiation":                       {1029, "5.3.28", "Enumerated", true},
	"QoS-Upgrade":                           {1030, "5.3.29", "Enumerated", true},
	"RAN-NAS-Release-Cause":                 {2819, "5.3.106", "OctetString", false},
	"RAN-Rule-Support":                      {2832, "5.3.122", "Unsigned32", false},
	"RAT-Type":                              {1032, "5.3.31", "Enumerated", false},
	"Redirect-Information":                  {1085, "5.3.82", "Grouped", false},
	"Redirect-Support":                      {1086, "5.3.83", "Enumerated", false},
	"Removal-Of-Access":                     {2842, "5.3.126", "Enumerated", false},
	"Reporting-Level":                       {1011, "5.3.12", "Enumerated", true},
	"Resource-Allocation-Notification":      {1063, "5.3.50", "Enumerated", false},
	"Resource-Release-Notification":         {2841, "5.3.125", "Enumerated", false},
	"Revalidation-Time":                     {1042, "5.3.41", "Time", true},
	"Routing-Filter":                        {1078, "5.3.72", "Grouped", false},
	"Routing-IP-Address":                    {1079, "5.3.73", "Address", false},
	"Routing-Rule-Definition":               {1076, "5.3.70", "Grouped", false},
	"Routing-Rule-Failure-Code":             {2834, "5.3.119", "Unsigned32", false},
	"Routing-Rule-Identifier":               {1077, "5.3.71", "OctetString", false},
	"Routing-Rule-Install":                  {1081, "5.3.68", "Grouped", false},
	"Routing-Rule-Remove":                   {1075, "5.3.69", "Grouped", false},
	"Routing-Rule-Report":                   {2835, "5.3.118", "Grouped", false},
	"Rule-Activation-Time":                  {1043, "5.3.42", "Time", true},
	"Rule-Deactivation-Time":                {1044, "5.3.43", "Time", true},
	"Rule-Failure-Code":                     {1031, "5.3.38", "Enumerated", true},
	"Security-Parameter-Index":              {1056, "5.3.51", "OctetString", false},
	"Session-Release-Cause":                 {1045, "5.3.44", "Enumerated", true},
	"TCP-Source-Port":                       {2843, "5.3.129", "Unsigned32", false},
	"TDF-Application-Identifier":            {1088, "5.3.77", "OctetString", false},
	"TDF-Application-Instance-Identifier":   {2802, "5.3.92", "OctetString", false},
	"TDF-Destination-Host":                  {1089, "5.3.80", "DiameterIdentity", false},
	"TDF-Destination-Realm":                 {1090, "5.3.79", "DiameterIdentity", false},
	"TDF-IP-Address":                        {1091, "5.3.81", "Address", false},
	"TDF-Information":                       {1087, "5.3.78", "Grouped", false},
	"TFT-Filter":                            {1012, "5.3.13", "IPFilterRule", true},
	"TFT-Packet-Filter-Information":         {1013, "5.3.14", "Grouped", true},
	"ToS-Traffic-Class":                     {1014, "5.3.15", "OctetString", true},
	"Traffic-Steering-Policy-Identifier-DL": {2836, "5.3.123", "OctetString", false},
	"Traffic-Steering-Policy-Identifier-UL": {2837, "5.3.124", "OctetString", false},
	"Tunnel-Header-Filter":                  {1036, "5.3.34", "IPFilterRule", false},
	"Tunnel-Header-Length":                  {1037, "5.3.35", "Unsigned32", false},
	"Tunnel-Information":                    {1038, "5.3.36", "Grouped", false},
	"UDP-Source-Port":                       {2806, "5.3.97", "Unsigned32", false},
	"UE-Local-IP-Address":                   {2805, "5.3.96", "Address", false},
	"UE-Status":                             {2854, "5.3.140", "Unsigned32", false},
	"Usage-Monitoring-Information":          {1067, "5.3.60", "Grouped", false},
	"Usage-Monitoring-Level":                {1068, "5.3.61", "Enumerated", false},
	"Usage-Monitoring-Report":               {1069, "5.3.62", "Enumerated", false},
	"Usage-Monitoring-Support":              {1070, "5.3.63", "Enumerated", false},
	"User-Location-Info-Time":               {2812, "5.3.101", "Time", false},
}

// reused29212 holds the vendor-3GPP AVPs of other specifications the
// Gx encoder writes (TS 29.212 Table 5.4.0.1 gives no M-bit override for
// them): the M bit of their defining specification.
var reused29212 = map[uint32]reusedAVP{
	500: {"Abort-Cause", "TS 29.214 Table 5.3.0.1", true},
	507: {"Flow-Description", "TS 29.214 Table 5.3.0.1", true},
	511: {"Flow-Status", "TS 29.214 Table 5.3.0.1", true},
	515: {"Max-Requested-Bandwidth-DL", "TS 29.214", true},
	516: {"Max-Requested-Bandwidth-UL", "TS 29.214", true},
}

type specAVP struct {
	Code   uint32
	Clause string
	Type   string
	MustM  bool
}

type reusedAVP struct {
	Name  string
	Spec  string
	MustM bool
}

var spec29212ByCode = func() map[uint32]specAVP {
	m := make(map[uint32]specAVP, len(spec29212))
	for _, a := range spec29212 {
		m[a.Code] = a
	}
	return m
}()

// avpGx returns a vendor-3GPP AVP with the V bit and the M bit its
// specification gives it: TS 29.212 Table 5.3.0.1 for the Gx AVPs,
// reused29212 for the others. An AVP in neither table is sent without M.
func avpGx(code uint32, data datatype.Type) *diam.AVP {
	flags := uint8(avp.Vbit)
	if a, ok := spec29212ByCode[code]; ok && a.MustM {
		flags |= avp.Mbit
	} else if r, ok := reused29212[code]; ok && r.MustM {
		flags |= avp.Mbit
	}
	return diam.NewAVP(code, flags, vendorID3GPP, data)
}
