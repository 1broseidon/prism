Internet-Draft The OAuth 2.1 Authorization Framework March 2026 Hardt, et al. Expires 3 September 2026 \[Page]

## [Abstract](#abstract)

The OAuth 2.1 authorization framework enables an application to obtain limited access to a protected resource, either on behalf of a resource owner by orchestrating an approval interaction between the resource owner and an authorization service, or by allowing the application to obtain access on its own behalf. This specification replaces and obsoletes the OAuth 2.0 Authorization Framework described in RFC 6749 and the Bearer Token Usage in RFC 6750.[¶](#section-abstract-1)

## [Discussion Venues](#name-discussion-venues)

This note is to be removed before publishing as an RFC.[¶](#section-note.1-1)

Discussion of this document takes place on the OAuth Working Group mailing list (oauth@ietf.org), which is archived at [https://mailarchive.ietf.org/arch/browse/oauth/](https://mailarchive.ietf.org/arch/browse/oauth/).[¶](#section-note.1-2)

Source for this draft and an issue tracker can be found at [https://github.com/oauth-wg/oauth-v2-1](https://github.com/oauth-wg/oauth-v2-1).[¶](#section-note.1-3)

## [Status of This Memo](#name-status-of-this-memo)

This Internet-Draft is submitted in full conformance with the provisions of BCP 78 and BCP 79.[¶](#section-boilerplate.1-1)

Internet-Drafts are working documents of the Internet Engineering Task Force (IETF). Note that other groups may also distribute working documents as Internet-Drafts. The list of current Internet-Drafts is at [https://datatracker.ietf.org/drafts/current/](https://datatracker.ietf.org/drafts/current/).[¶](#section-boilerplate.1-2)

Internet-Drafts are draft documents valid for a maximum of six months and may be updated, replaced, or obsoleted by other documents at any time. It is inappropriate to use Internet-Drafts as reference material or to cite them other than as "work in progress."[¶](#section-boilerplate.1-3)

This Internet-Draft will expire on 3 September 2026.[¶](#section-boilerplate.1-4)

## [Copyright Notice](#name-copyright-notice)

Copyright (c) 2026 IETF Trust and the persons identified as the document authors. All rights reserved.[¶](#section-boilerplate.2-1)

This document is subject to BCP 78 and the IETF Trust's Legal Provisions Relating to IETF Documents ([https://trustee.ietf.org/license-info](https://trustee.ietf.org/license-info)) in effect on the date of publication of this document. Please review these documents carefully, as they describe your rights and restrictions with respect to this document. Code Components extracted from this document must include Revised BSD License text as described in Section 4.e of the Trust Legal Provisions and are provided without warranty as described in the Revised BSD License.[¶](#section-boilerplate.2-2)

[▲](#)

## [Table of Contents](#name-table-of-contents)

- [1](#section-1).  [Introduction](#name-introduction)
  
  - [1.1](#section-1.1).  [Roles](#name-roles)
  - [1.2](#section-1.2).  [Protocol Flow](#name-protocol-flow)
  - [1.3](#section-1.3).  [Authorization Grant](#name-authorization-grant)
    
    - [1.3.1](#section-1.3.1).  [Authorization Code](#name-authorization-code)
    - [1.3.2](#section-1.3.2).  [Refresh Token](#name-refresh-token)
    - [1.3.3](#section-1.3.3).  [Client Credentials](#name-client-credentials)
  - [1.4](#section-1.4).  [Access Token](#name-access-token)
    
    - [1.4.1](#section-1.4.1).  [Access Token Scope](#name-access-token-scope)
    - [1.4.2](#section-1.4.2).  [Bearer Tokens](#name-bearer-tokens)
    - [1.4.3](#section-1.4.3).  [Sender-Constrained Access Tokens](#name-sender-constrained-access-t)
  - [1.5](#section-1.5).  [Communication security](#name-communication-security)
  - [1.6](#section-1.6).  [HTTP Redirections](#name-http-redirections)
  - [1.7](#section-1.7).  [Interoperability](#name-interoperability)
  - [1.8](#section-1.8).  [Compatibility with OAuth 2.0](#name-compatibility-with-oauth-20)
  - [1.9](#section-1.9).  [Notational Conventions](#name-notational-conventions)
- [2](#section-2).  [Client Registration](#name-client-registration)
  
  - [2.1](#section-2.1).  [Client Types](#name-client-types)
  - [2.2](#section-2.2).  [Client Identifier](#name-client-identifier)
  - [2.3](#section-2.3).  [Client Redirection Endpoint](#name-client-redirection-endpoint)
    
    - [2.3.1](#section-2.3.1).  [Registration Requirements](#name-registration-requirements)
    - [2.3.2](#section-2.3.2).  [Multiple Redirect URIs](#name-multiple-redirect-uris)
    - [2.3.3](#section-2.3.3).  [Preventing CSRF Attacks](#name-preventing-csrf-attacks)
    - [2.3.4](#section-2.3.4).  [Preventing Mix-Up Attacks](#name-preventing-mix-up-attacks)
    - [2.3.5](#section-2.3.5).  [Invalid Endpoint](#name-invalid-endpoint)
    - [2.3.6](#section-2.3.6).  [Endpoint Content](#name-endpoint-content)
  - [2.4](#section-2.4).  [Client Authentication](#name-client-authentication)
    
    - [2.4.1](#section-2.4.1).  [Client Secret](#name-client-secret)
    - [2.4.2](#section-2.4.2).  [Other Authentication Methods](#name-other-authentication-method)
  - [2.5](#section-2.5).  [Unregistered Clients](#name-unregistered-clients)
- [3](#section-3).  [Protocol Endpoints](#name-protocol-endpoints)
  
  - [3.1](#section-3.1).  [Authorization Endpoint](#name-authorization-endpoint)
  - [3.2](#section-3.2).  [Token Endpoint](#name-token-endpoint)
    
    - [3.2.1](#section-3.2.1).  [Client Authentication](#name-client-authentication-2)
    - [3.2.2](#section-3.2.2).  [Token Endpoint Request](#name-token-endpoint-request)
    - [3.2.3](#section-3.2.3).  [Token Endpoint Response](#name-token-endpoint-response)
    - [3.2.4](#section-3.2.4).  [Token Endpoint Error Response](#name-token-endpoint-error-respon)
- [4](#section-4).  [Grant Types](#name-grant-types)
  
  - [4.1](#section-4.1).  [Authorization Code Grant](#name-authorization-code-grant)
    
    - [4.1.1](#section-4.1.1).  [Authorization Request](#name-authorization-request)
    - [4.1.2](#section-4.1.2).  [Authorization Response](#name-authorization-response)
    - [4.1.3](#section-4.1.3).  [Token Endpoint Extension](#name-token-endpoint-extension)
  - [4.2](#section-4.2).  [Client Credentials Grant](#name-client-credentials-grant)
    
    - [4.2.1](#section-4.2.1).  [Token Endpoint Extension](#name-token-endpoint-extension-2)
  - [4.3](#section-4.3).  [Refresh Token Grant](#name-refresh-token-grant)
    
    - [4.3.1](#section-4.3.1).  [Token Endpoint Extension](#name-token-endpoint-extension-3)
    - [4.3.2](#section-4.3.2).  [Refresh Token Response](#name-refresh-token-response)
    - [4.3.3](#section-4.3.3).  [Refresh Token Recommendations](#name-refresh-token-recommendatio)
  - [4.4](#section-4.4).  [Extension Grants](#name-extension-grants)
- [5](#section-5).  [Resource Requests](#name-resource-requests)
  
  - [5.1](#section-5.1).  [Bearer Token Requests](#name-bearer-token-requests)
    
    - [5.1.1](#section-5.1.1).  [Authorization Request Header Field](#name-authorization-request-heade)
    - [5.1.2](#section-5.1.2).  [Form-Encoded Content Parameter](#name-form-encoded-content-parame)
  - [5.2](#section-5.2).  [Access Token Validation](#name-access-token-validation)
  - [5.3](#section-5.3).  [Error Response](#name-error-response)
    
    - [5.3.1](#section-5.3.1).  [The WWW-Authenticate Response Header Field](#name-the-www-authenticate-respon)
    - [5.3.2](#section-5.3.2).  [Error Codes](#name-error-codes)
- [6](#section-6).  [Extensibility](#name-extensibility)
  
  - [6.1](#section-6.1).  [Defining Access Token Types](#name-defining-access-token-types)
    
    - [6.1.1](#section-6.1.1).  [Registered Access Token Types](#name-registered-access-token-typ)
    - [6.1.2](#section-6.1.2).  [Vendor-Specific Access Token Types](#name-vendor-specific-access-toke)
  - [6.2](#section-6.2).  [Defining New Endpoint Parameters](#name-defining-new-endpoint-param)
  - [6.3](#section-6.3).  [Defining New Authorization Grant Types](#name-defining-new-authorization-)
  - [6.4](#section-6.4).  [Defining New Authorization Endpoint Response Types](#name-defining-new-authorization-e)
  - [6.5](#section-6.5).  [Defining Additional Error Codes](#name-defining-additional-error-c)
- [7](#section-7).  [Security Considerations](#name-security-considerations)
  
  - [7.1](#section-7.1).  [Access Token Security Considerations](#name-access-token-security-consi)
    
    - [7.1.1](#section-7.1.1).  [Security Threats](#name-security-threats)
    - [7.1.2](#section-7.1.2).  [Threat Mitigation](#name-threat-mitigation)
    - [7.1.3](#section-7.1.3).  [Summary of Recommendations](#name-summary-of-recommendations)
    - [7.1.4](#section-7.1.4).  [Access Token Privilege Restriction](#name-access-token-privilege-rest)
  - [7.2](#section-7.2).  [Client Authentication](#name-client-authentication-3)
  - [7.3](#section-7.3).  [Client Impersonation](#name-client-impersonation)
    
    - [7.3.1](#section-7.3.1).  [Impersonation of Native Apps](#name-impersonation-of-native-app)
    - [7.3.2](#section-7.3.2).  [Access Token Privilege Restriction](#name-access-token-privilege-restr)
  - [7.4](#section-7.4).  [Client Impersonating Resource Owner](#name-client-impersonating-resour)
  - [7.5](#section-7.5).  [Authorization Code Security Considerations](#name-authorization-code-security)
    
    - [7.5.1](#section-7.5.1).  [Authorization Code Injection](#name-authorization-code-injectio)
    - [7.5.2](#section-7.5.2).  [Reuse of Authorization Codes](#name-reuse-of-authorization-code)
    - [7.5.3](#section-7.5.3).  [HTTP 307 Redirect](#name-http-307-redirect)
  - [7.6](#section-7.6).  [Ensuring Endpoint Authenticity](#name-ensuring-endpoint-authentic)
  - [7.7](#section-7.7).  [Credentials-Guessing Attacks](#name-credentials-guessing-attack)
  - [7.8](#section-7.8).  [Phishing Attacks](#name-phishing-attacks)
  - [7.9](#section-7.9).  [Cross-Site Request Forgery](#name-cross-site-request-forgery)
  - [7.10](#section-7.10). [Clickjacking](#name-clickjacking)
  - [7.11](#section-7.11). [Injection and Input Validation](#name-injection-and-input-validat)
  - [7.12](#section-7.12). [Open Redirection](#name-open-redirection)
    
    - [7.12.1](#section-7.12.1).  [Client as Open Redirector](#name-client-as-open-redirector)
    - [7.12.2](#section-7.12.2).  [Authorization Server as Open Redirector](#name-authorization-server-as-ope)
  - [7.13](#section-7.13). [Transport Security](#name-transport-security)
  - [7.14](#section-7.14). [Authorization Server Mix-Up Mitigation](#name-authorization-server-mix-up)
    
    - [7.14.1](#section-7.14.1).  [Mix-Up Defense via Issuer Identification](#name-mix-up-defense-via-issuer-i)
    - [7.14.2](#section-7.14.2).  [Mix-Up Defense via Distinct Redirect URIs](#name-mix-up-defense-via-distinct)
- [8](#section-8).  [Native Applications](#name-native-applications)
  
  - [8.1](#section-8.1).  [Client Authentication of Native Apps](#name-client-authentication-of-na)
    
    - [8.1.1](#section-8.1.1).  [Registration of Native App Clients](#name-registration-of-native-app-)
    - [8.1.2](#section-8.1.2).  [Native App Attestation](#name-native-app-attestation)
  - [8.2](#section-8.2).  [Using Inter-App URI Communication for OAuth in Native Apps](#name-using-inter-app-uri-communi)
  - [8.3](#section-8.3).  [Initiating the Authorization Request from a Native App](#name-initiating-the-authorizatio)
  - [8.4](#section-8.4).  [Receiving the Authorization Response in a Native App](#name-receiving-the-authorization)
    
    - [8.4.1](#section-8.4.1).  [Claimed "https" Scheme URI Redirection](#name-claimed-https-scheme-uri-re)
    - [8.4.2](#section-8.4.2).  [Loopback Interface Redirection](#name-loopback-interface-redirect)
    - [8.4.3](#section-8.4.3).  [Private-Use URI Scheme Redirection](#name-private-use-uri-scheme-redi)
  - [8.5](#section-8.5).  [Security Considerations in Native Apps](#name-security-considerations-in-)
    
    - [8.5.1](#section-8.5.1).  [Embedded User Agents in Native Apps](#name-embedded-user-agents-in-nat)
    - [8.5.2](#section-8.5.2).  [Fake External User-Agents in Native Apps](#name-fake-external-user-agents-i)
    - [8.5.3](#section-8.5.3).  [Malicious External User-Agents in Native Apps](#name-malicious-external-user-age)
    - [8.5.4](#section-8.5.4).  [Loopback Redirect Considerations in Native Apps](#name-loopback-redirect-considera)
- [9](#section-9).  [Browser-Based Apps](#name-browser-based-apps)
- [10](#section-10). [Differences from OAuth 2.0](#name-differences-from-oauth-20)
  
  - [10.1](#section-10.1).  [Removal of the OAuth 2.0 Implicit grant](#name-removal-of-the-oauth-20-imp)
  - [10.2](#section-10.2).  [Redirect URI Parameter in Token Request](#name-redirect-uri-parameter-in-t)
- [11](#section-11). [IANA Considerations](#name-iana-considerations)
- [12](#section-12). [References](#name-references)
  
  - [12.1](#section-12.1).  [Normative References](#name-normative-references)
  - [12.2](#section-12.2).  [Informative References](#name-informative-references)
- [Appendix A](#appendix-A).  [Augmented Backus-Naur Form (ABNF) Syntax](#name-augmented-backus-naur-form-)
  
  - [A.1](#appendix-A.1).  ["client\_id" Syntax](#name-client_id-syntax)
  - [A.2](#appendix-A.2).  ["client\_secret" Syntax](#name-client_secret-syntax)
  - [A.3](#appendix-A.3).  ["response\_type" Syntax](#name-response_type-syntax)
  - [A.4](#appendix-A.4).  ["scope" Syntax](#name-scope-syntax)
  - [A.5](#appendix-A.5).  ["state" Syntax](#name-state-syntax)
  - [A.6](#appendix-A.6).  ["redirect\_uri" Syntax](#name-redirect_uri-syntax)
  - [A.7](#appendix-A.7).  ["error" Syntax](#name-error-syntax)
  - [A.8](#appendix-A.8).  ["error\_description" Syntax](#name-error_description-syntax)
  - [A.9](#appendix-A.9).  ["error\_uri" Syntax](#name-error_uri-syntax)
  - [A.10](#appendix-A.10). ["grant\_type" Syntax](#name-grant_type-syntax)
  - [A.11](#appendix-A.11). ["code" Syntax](#name-code-syntax)
  - [A.12](#appendix-A.12). ["access\_token" Syntax](#name-access_token-syntax)
  - [A.13](#appendix-A.13). ["token\_type" Syntax](#name-token_type-syntax)
  - [A.14](#appendix-A.14). ["expires\_in" Syntax](#name-expires_in-syntax)
  - [A.15](#appendix-A.15). ["refresh\_token" Syntax](#name-refresh_token-syntax)
  - [A.16](#appendix-A.16). [Endpoint Parameter Syntax](#name-endpoint-parameter-syntax)
  - [A.17](#appendix-A.17). ["code\_verifier" Syntax](#name-code_verifier-syntax)
  - [A.18](#appendix-A.18). ["code\_challenge" Syntax](#name-code_challenge-syntax)
- [Appendix B](#appendix-B).  [Use of application/x-www-form-urlencoded Media Type](#name-use-of-application-x-www-fo)
- [Appendix C](#appendix-C).  [Serializations](#name-serializations)
  
  - [C.1](#appendix-C.1).  [Query String Serialization](#name-query-string-serialization)
  - [C.2](#appendix-C.2).  [Form-Encoded Serialization](#name-form-encoded-serialization)
  - [C.3](#appendix-C.3).  [JSON Serialization](#name-json-serialization)
- [Appendix D](#appendix-D).  [Extensions](#name-extensions)
- [Appendix E](#appendix-E).  [Acknowledgements](#name-acknowledgements)
- [Appendix F](#appendix-F).  [Document History](#name-document-history)
- [](#appendix-G)[Authors' Addresses](#name-authors-addresses)

## [1.](#section-1) [Introduction](#name-introduction)

OAuth introduces an authorization layer to the client-server authentication model by separating the role of the client from that of the resource owner. In OAuth, the client requests access to resources controlled by the resource owner and hosted by the resource server. Instead of using the resource owner's credentials to access protected resources, the client obtains an access token - a credential representing a specific set of access attributes such as scope and lifetime. Access tokens are issued to clients by an authorization server with the approval of the resource owner. The client uses the access token to access the protected resources hosted by the resource server.[¶](#section-1-1)

In the older, more limited client-server authentication model, the client requests an access-restricted resource (protected resource) on the server by authenticating to the server using the resource owner's credentials. In order to provide applications access to restricted resources, the resource owner shares their credentials with the application. This creates several problems and limitations:[¶](#section-1-2)

- Applications are required to store the resource owner's credentials for future use, typically a password in clear-text.[¶](#section-1-3.1.1)
- Servers are required to support password authentication, despite the security weaknesses inherent in passwords.[¶](#section-1-3.2.1)
- Applications gain overly broad access to the resource owner's protected resources, leaving resource owners without any ability to restrict duration or access to a limited subset of resources.[¶](#section-1-3.3.1)
- Resource owners often reuse passwords with other unrelated services, despite best security practices. This password reuse means a vulnerability or exposure in one service may have security implications in completely unrelated services.[¶](#section-1-3.4.1)
- Resource owners cannot revoke access to an individual application without revoking access to all third parties, and must do so by changing their password.[¶](#section-1-3.5.1)
- Compromise of any application results in compromise of the end-user's password and all of the data protected by that password.[¶](#section-1-3.6.1)

An example where OAuth is used is where an end user (resource owner) grants a financial management service (client) access to their sensitive transaction history stored at a banking service (resource server), without sharing their username and password with the financial management service. Instead, they authenticate directly with their financial institution's server (authorization server), which issues the financial management service delegation-specific credentials (access token).[¶](#section-1-4)

This separation of concerns also provides the ability to use more advanced user authentication methods such as multi-factor authentication and even passwordless authentication, without any modification to the applications. With all user authentication logic handled by the authorization server, applications don't need to be concerned with the specifics of implementing any particular authentication mechanism. This provides the ability for the authorization server to manage the user authentication policies and even change them in the future without coordinating the changes with applications.[¶](#section-1-5)

The authorization layer can also simplify how a resource server determines if a request is authorized. Traditionally, after authenticating the client, each resource server would evaluate policies to compute if the client is authorized on each API call. In a distributed system, the policies need to be synchronized to all the resource servers, or the resource server must call a central policy server to process each request. In OAuth, evaluation of the policies is performed only when a new access token is created by the authorization server. If the authorized access is represented in the access token, the resource server no longer needs to evaluate the policies, and only needs to validate the access token. This simplification applies when the application is acting on behalf of a resource owner, or on behalf of itself.[¶](#section-1-6)

OAuth is an authorization protocol, not an authentication protocol, as OAuth does not define the necessary components to achieve user authentication. An authentication protocol is necessary if the goal is to authenticate users. An example is OpenID Connect \[[OpenID.Connect](#OpenID.Connect)], which builds on OAuth to provide the security characteristics and necessary components required of an authentication protocol.[¶](#section-1-7)

The access token represents the authorization granted to the client. It is a common practice for the client to present the access token to a proprietary API which returns a user identifier for the resource owner, and then using the result of the API as a proxy for authenticating the user. This practice is not part of the OAuth standard or security considerations, and may not have been considered by the resource owner. Implementors should carefully consult the documentation of the resource server before adopting this practice.[¶](#section-1-8)

This specification is designed for use with HTTP \[[RFC9110](#RFC9110)]. The use of OAuth over any protocol other than HTTP is out of scope.[¶](#section-1-9)

Since the publication of the OAuth 2.0 Authorization Framework \[[RFC6749](#RFC6749)] in October 2012, it has been updated by OAuth 2.0 for Native Apps \[[RFC8252](#RFC8252)], OAuth Security Best Current Practice \[[RFC9700](#RFC9700)], and OAuth 2.0 for Browser-Based Apps \[[I-D.ietf-oauth-browser-based-apps](#I-D.ietf-oauth-browser-based-apps)]. The OAuth 2.0 Authorization Framework: Bearer Token Usage \[[RFC6750](#RFC6750)] has also been updated with \[[RFC9700](#RFC9700)]. This Standards Track specification consolidates the information in all of these documents and removes features that have been found to be insecure in \[[RFC9700](#RFC9700)].[¶](#section-1-10)

### [1.1.](#section-1.1) [Roles](#name-roles)

OAuth defines four roles:[¶](#section-1.1-1)

"resource owner":

An entity capable of granting access to a protected resource. When the resource owner is a person, it is referred to as an end user. This is sometimes abbreviated as "RO".[¶](#section-1.1-2.2.1)

"resource server":

The server hosting the protected resources, capable of accepting and responding to protected resource requests using access tokens. The resource server is often accessible via an API. This is sometimes abbreviated as "RS".[¶](#section-1.1-2.4.1)

"client":

An application making protected resource requests on behalf of the resource owner and with its authorization. The term "client" does not imply any particular implementation characteristics (e.g., whether the application executes on a server, a desktop, or other devices).[¶](#section-1.1-2.6.1)

"authorization server":

The server issuing access tokens to the client after successfully authenticating the resource owner and obtaining authorization. This is sometimes abbreviated as "AS".[¶](#section-1.1-2.8.1)

Most of this specification defines the interaction between the client and the authorization server, as well as between the client and resource server.[¶](#section-1.1-3)

The interaction between the authorization server and resource server is beyond the scope of this specification, however several extensions have been defined to provide an option for interoperability between resource servers and authorization servers. The authorization server may be the same server as the resource server or a separate entity. A single authorization server may issue access tokens accepted by multiple resource servers.[¶](#section-1.1-4)

The interaction between the resource owner and authorization server (e.g. how the end user authenticates themselves at the authorization server) is also out of scope of this specification, with some exceptions, such as security considerations around prompting the end user for consent.[¶](#section-1.1-5)

When the resource owner is the end user, the user will interact with the client. When the client is a web-based application, the user will interact with the client through a user agent (as described in [Section 3.5](https://rfc-editor.org/rfc/rfc9110#section-3.5) of \[[RFC9110](#RFC9110)]). When the client is a native application, the user will interact with the client directly through the operating system. See [Section 2.1](#client-types) for further details.[¶](#section-1.1-6)

### [1.2.](#section-1.2) [Protocol Flow](#name-protocol-flow)

```
     +--------+                               +---------------+
     |        |--(1)- Authorization Request ->|   Resource    |
     |        |                               |     Owner     |
     |        |<-(2)-- Authorization Grant ---|               |
     |        |                               +---------------+
     |        |
     |        |                               +---------------+
     |        |--(3)-- Authorization Grant -->| Authorization |
     | Client |                               |     Server    |
     |        |<-(4)----- Access Token -------|               |
     |        |                               +---------------+
     |        |
     |        |                               +---------------+
     |        |--(5)----- Access Token ------>|    Resource   |
     |        |                               |     Server    |
     |        |<-(6)--- Protected Resource ---|               |
     +--------+                               +---------------+
```

[Figure 1](#figure-1): [Abstract Protocol Flow](#name-abstract-protocol-flow)

The abstract OAuth 2.1 flow illustrated in [Figure 1](#fig-protocol-flow) describes the interaction between the four roles and includes the following steps:[¶](#section-1.2-2)

1. The client requests authorization from the resource owner. The authorization request can be made directly to the resource owner (as shown), or preferably indirectly via the authorization server as an intermediary.[¶](#section-1.2-3.1.1)
2. The client receives an authorization grant, which is a credential representing the resource owner's authorization, expressed using one of the authorization grant types defined in this specification or using an extension grant type. The authorization grant type depends on the method used by the client to request authorization and the types supported by the authorization server.[¶](#section-1.2-3.2.1)
3. The client requests an access token by authenticating with the authorization server and presenting the authorization grant.[¶](#section-1.2-3.3.1)
4. The authorization server authenticates the client and validates the authorization grant, and if valid, issues an access token.[¶](#section-1.2-3.4.1)
5. The client requests the protected resource from the resource server and authenticates by presenting the access token.[¶](#section-1.2-3.5.1)
6. The resource server validates the access token, and if valid, serves the request.[¶](#section-1.2-3.6.1)

The preferred method for the client to obtain an authorization grant from the resource owner (depicted in steps (1) and (2)) is to use the authorization server as an intermediary, which is illustrated in [Figure 3](#fig-authorization-code-flow) in [Section 4.1](#authorization-code-grant).[¶](#section-1.2-4)

### [1.3.](#section-1.3) [Authorization Grant](#name-authorization-grant)

An authorization grant represents the resource owner's authorization (to access its protected resources) used by the client to obtain an access token. This specification defines three grant types -- authorization code, refresh token, and client credentials -- as well as an extensibility mechanism for defining additional types.[¶](#section-1.3-1)

#### [1.3.1.](#section-1.3.1) [Authorization Code](#name-authorization-code)

An authorization code is a temporary credential used to obtain an access token. Instead of the client requesting authorization directly from the resource owner, the client directs the resource owner to an authorization server (via its user agent) which in turn directs the resource owner back to the client with the authorization code. The client can then exchange the authorization code for an access token.[¶](#section-1.3.1-1)

Before directing the resource owner back to the client with the authorization code, the authorization server authenticates the resource owner, and may request the resource owner's consent or otherwise inform them of the client's request. Because the resource owner only authenticates with the authorization server, the resource owner's credentials are never shared with the client, and the client does not need to have knowledge of any additional authentication steps such as multi-factor authentication or delegated accounts.[¶](#section-1.3.1-2)

The authorization code provides a few important security benefits, such as the ability to authenticate the client, as well as the transmission of the access token directly to the client without passing it through the resource owner's user agent and potentially exposing it to others, including the resource owner.[¶](#section-1.3.1-3)

#### [1.3.2.](#section-1.3.2) [Refresh Token](#name-refresh-token)

Refresh tokens are credentials used to obtain access tokens. Refresh tokens may be issued to the client by the authorization server and are used to obtain a new access token when the current access token becomes invalid or expires, or to obtain additional access tokens with identical or narrower scope (access tokens may have a shorter lifetime and fewer privileges than authorized by the resource owner). Issuing a refresh token is optional at the discretion of the authorization server, and may be issued based on properties of the client, properties of the request, policies within the authorization server, or any other criteria. If the authorization server issues a refresh token, it is included when issuing an access token (i.e., step (2) in [Figure 2](#fig-refresh-token-flow)). The lifetime of the refresh token is also at the discretion of the authorization server.[¶](#section-1.3.2-1)

A refresh token is a string representing the authorization granted to the client by the resource owner. The string is considered opaque to the client. The refresh token may be an identifier used to retrieve the authorization information or may encode this information into the string itself. Unlike access tokens, refresh tokens are intended for use only with authorization servers and are never sent to resource servers.[¶](#section-1.3.2-2)

```
+--------+                                           +---------------+
|        |--(1)------- Authorization Grant --------->|               |
|        |                                           |               |
|        |<-(2)----------- Access Token -------------|               |
|        |               & Refresh Token             |               |
|        |                                           |               |
|        |                            +----------+   |               |
|        |--(3)---- Access Token ---->|          |   |               |
|        |                            |          |   |               |
|        |<-(4)- Protected Resource --| Resource |   | Authorization |
| Client |                            |  Server  |   |     Server    |
|        |--(5)---- Access Token ---->|          |   |               |
|        |                            |          |   |               |
|        |<-(6)- Invalid Token Error -|          |   |               |
|        |                            +----------+   |               |
|        |                                           |               |
|        |--(7)----------- Refresh Token ----------->|               |
|        |                                           |               |
|        |<-(8)----------- Access Token -------------|               |
+--------+           & Optional Refresh Token        +---------------+
```

[Figure 2](#figure-2): [Refreshing an Expired Access Token](#name-refreshing-an-expired-acces)

The flow illustrated in [Figure 2](#fig-refresh-token-flow) includes the following steps:[¶](#section-1.3.2-4)

1. The client requests an access token by authenticating with the authorization server and presenting an authorization grant.[¶](#section-1.3.2-5.1.1)
2. The authorization server authenticates the client and validates the authorization grant, and if valid, issues an access token and optionally a refresh token.[¶](#section-1.3.2-5.2.1)
3. The client makes a protected resource request to the resource server by presenting the access token.[¶](#section-1.3.2-5.3.1)
4. The resource server validates the access token, and if valid, serves the request.[¶](#section-1.3.2-5.4.1)
5. Steps (3) and (4) repeat until the access token expires. If the client knows the access token expired, it skips to step (7); otherwise, it makes another protected resource request.[¶](#section-1.3.2-5.5.1)
6. Since the access token is invalid, the resource server returns an invalid token error.[¶](#section-1.3.2-5.6.1)
7. The client requests a new access token by presenting the refresh token and providing client authentication if it has been issued credentials. The client authentication requirements are based on the client type and on the authorization server policies.[¶](#section-1.3.2-5.7.1)
8. The authorization server authenticates the client and validates the refresh token, and if valid, issues a new access token (and, optionally, a new refresh token).[¶](#section-1.3.2-5.8.1)

Note that there is no need to communicate the lifetime of the refresh token to the client, because the client can't do anything different with the knowledge of the lifetime. Additionally, the authorization server might choose to use dynamic lifetimes (e.g. the refresh token expiry is extended as long as the refresh token is used at least once every 7 days), or the authorization server might revoke the refresh token before its scheduled expiration date for any reason, such as if the user revokes the application's access. This means the client already has to handle the case of a refresh token expiring at an arbitrary time.[¶](#section-1.3.2-6)

Regardless of why or when the refresh token expires, the client has only one path to obtain new tokens, which is to start a new OAuth flow from the beginning. For that reason, there is no property defined to communicate the expiration of a refresh token to the client.[¶](#section-1.3.2-7)

#### [1.3.3.](#section-1.3.3) [Client Credentials](#name-client-credentials)

The client credentials or other forms of client authentication (e.g., a private key used to sign a JWT, as described in \[[RFC7523](#RFC7523)] and its update \[[I-D.ietf-oauth-rfc7523bis](#I-D.ietf-oauth-rfc7523bis)]) can be used as an authorization grant when the authorization scope is limited to the protected resources under the control of the client, or to protected resources previously arranged with the authorization server. Client credentials are used when the client is requesting access to protected resources based on an authorization previously arranged with the authorization server.[¶](#section-1.3.3-1)

### [1.4.](#section-1.4) [Access Token](#name-access-token)

Access tokens are credentials used to access protected resources. An access token is a string representing an authorization issued to the client.[¶](#section-1.4-1)

The string is considered opaque to the client, even if it has a structure. The client MUST NOT expect to be able to parse the access token value. The authorization server is not required to use a consistent access token encoding or format other than what is expected by the resource server.[¶](#section-1.4-2)

The access granted by the resource owner to the client is represented by the Access Token created by the authorization server. Access Tokens are short lived to reduce the blast radius of a leaked Access Token. The expiration of the Access Token is set by the authorization server.[¶](#section-1.4-3)

Depending on the authorization server implementation, the token string may be used by the resource server to retrieve the authorization information, or the token may self-contain the authorization information in a verifiable manner (i.e., a token string consisting of a signed data payload). One example of a token retrieval mechanism is Token Introspection \[[RFC7662](#RFC7662)], in which the RS calls an endpoint on the AS to validate the token presented by the client. One example of a structured token format is JWT Profile for Access Tokens \[[RFC9068](#RFC9068)], a method of encoding and signing access token data as a JSON Web Token \[[RFC7519](#RFC7519)].[¶](#section-1.4-4)

Additional authentication credentials, which are beyond the scope of this specification, may be required in order for the client to use an access token. This is typically referred to as a sender-constrained access token, such as DPoP \[[RFC9449](#RFC9449)] and Mutual TLS Certificate-Bound Access Tokens \[[RFC8705](#RFC8705)].[¶](#section-1.4-5)

The access token provides an abstraction layer, replacing different authorization constructs (e.g., username and password) with a single token understood by the resource server. This abstraction enables issuing access tokens more restrictive than the authorization grant used to obtain them, as well as removing the resource server's need to understand a wide range of authentication methods.[¶](#section-1.4-6)

Access tokens can have different formats, structures, and methods of utilization (e.g., cryptographic properties) based on the resource server security requirements. Access token attributes and the methods used to access protected resources may be extended beyond what is described in this specification.[¶](#section-1.4-7)

Access tokens (as well as any confidential access token attributes) MUST be kept confidential in transit and storage, and only shared among the authorization server, the resource servers the access token is valid for, and the client to which the access token is issued.[¶](#section-1.4-8)

The authorization server MUST ensure that access tokens cannot be generated, modified, or guessed to produce valid access tokens by unauthorized parties.[¶](#section-1.4-9)

#### [1.4.1.](#section-1.4.1) [Access Token Scope](#name-access-token-scope)

Access tokens are intended to be issued to clients with less privileges than the user granting the access has. This is known as a limited "scope" access token. The authorization server and resource server can use this scope mechanism to limit what types of resources or level of access a particular client can have.[¶](#section-1.4.1-1)

For example, a client may only need "read" access to a user's resources, but doesn't need to update resources, so the client can request the read-only scope defined by the authorization server, and obtain an access token that cannot be used to update resources. This requires coordination between the authorization server, resource server, and client. The authorization server provides the client the ability to request specific scopes, and associates those scopes with the access token issued to the client. The resource server is then responsible for enforcing scopes when presented with a limited-scope access token.[¶](#section-1.4.1-2)

OAuth does not define any scope values, instead scopes are defined by the authorization server or by extensions or profiles of OAuth. One such extension that defines scopes is \[[OpenID.Connect](#OpenID.Connect)], which defines a set of scopes that provide granular access to a user's profile information. It is recommended to avoid defining custom scopes that conflict with scopes from known extensions.[¶](#section-1.4.1-3)

To request a limited-scope access token, the client uses the `scope` request parameter at the authorization or token endpoints, depending on the grant type used. In turn, the authorization server uses the `scope` response parameter to inform the client of the scope of the access token issued.[¶](#section-1.4.1-4)

The value of the scope parameter is expressed as a space- delimited list of case-sensitive strings. The strings are defined by the authorization server. If the value contains multiple space-delimited strings, their order does not matter, and each string adds an additional access range to the requested scope.[¶](#section-1.4.1-5)

```
    scope       = scope-token *( SP scope-token )
    scope-token = 1*( %x21 / %x23-5B / %x5D-7E )
```

[¶](#section-1.4.1-6)

The authorization server MAY fully or partially ignore the scope requested by the client, based on the authorization server policy or the resource owner's instructions. If the issued access token scope is different from the one requested by the client, the authorization server MUST include the `scope` response parameter in the token response ([Section 3.2.3](#token-response)) to inform the client of the actual scope granted.[¶](#section-1.4.1-7)

If the client omits the scope parameter when requesting authorization, the authorization server MUST either process the request using a pre-defined default value or fail the request indicating an invalid scope. The authorization server SHOULD document its scope requirements and default value (if defined).[¶](#section-1.4.1-8)

#### [1.4.2.](#section-1.4.2) [Bearer Tokens](#name-bearer-tokens)

A Bearer Token is a security token with the property that any party in possession of the token (a "bearer") can use the token in any way that any other party in possession of it can. Using a Bearer Token does not require a bearer to prove possession of cryptographic key material (proof-of-possession).[¶](#section-1.4.2-1)

Bearer Tokens may be enhanced with proof-of-possession specifications such as DPoP \[[RFC9449](#RFC9449)] and mTLS \[[RFC8705](#RFC8705)] to provide proof-of-possession characteristics.[¶](#section-1.4.2-2)

To protect against access token disclosure, the communication interaction between the client and the resource server MUST utilize confidentiality and integrity protection as described in [Section 1.5](#communication-security).[¶](#section-1.4.2-3)

There is no requirement on the particular structure or format of a bearer token. If a bearer token is a reference to authorization information, such references MUST be infeasible for an attacker to guess, such as using a sufficiently long cryptographically random string. If a bearer token uses an encoding mechanism to contain the authorization information in the token itself, the access token MUST use integrity protection sufficient to prevent the token from being modified. One example of an encoding and signing mechanism for access tokens is described in JSON Web Token Profile for Access Tokens \[[RFC9068](#RFC9068)].[¶](#section-1.4.2-4)

#### [1.4.3.](#section-1.4.3) [Sender-Constrained Access Tokens](#name-sender-constrained-access-t)

A sender-constrained access token binds the use of an access token to a specific sender. This sender is obliged to demonstrate knowledge of a certain secret as prerequisite for the acceptance of that access token at the recipient (e.g., a resource server).[¶](#section-1.4.3-1)

Authorization and resource servers SHOULD use mechanisms for sender-constraining access tokens, such as OAuth Demonstration of Proof of Possession (DPoP) \[[RFC9449](#RFC9449)] or Mutual TLS for OAuth 2.0 \[[RFC8705](#RFC8705)]. See [Section 4.10.1](https://rfc-editor.org/rfc/rfc9700#section-4.10.1) of \[[RFC9700](#RFC9700)] to prevent misuse of stolen and leaked access tokens.[¶](#section-1.4.3-2)

It is RECOMMENDED to use end-to-end TLS between the client and the resource server. If TLS traffic needs to be terminated at an intermediary, refer to [Section 4.13](https://rfc-editor.org/rfc/rfc9700#section-4.13) of \[[RFC9700](#RFC9700)] for further security advice.[¶](#section-1.4.3-3)

### [1.5.](#section-1.5) [Communication security](#name-communication-security)

Implementations MUST use a mechanism to provide communication authentication, integrity and confidentiality such as Transport-Layer Security \[[RFC8446](#RFC8446)], to protect the exchange of clear-text credentials and tokens either in the content or in header fields from eavesdropping which enables replay (e.g., see [Section 2.4.1](#client-secret), [Section 7.5.1](#authorization_codes), [Section 3.2](#token-endpoint), and [Section 1.4.2](#bearer-tokens)).[¶](#section-1.5-1)

All the OAuth protocol URLs (URLs exposed by the AS, RS and Client) MUST use the `https` scheme except for loopback interface redirect URIs, which MAY use the `http` scheme. When using `https`, TLS certificates MUST be checked according to [Section 4.3.4](https://rfc-editor.org/rfc/rfc9110#section-4.3.4) of \[[RFC9110](#RFC9110)]. At the time of this writing, TLS version 1.3 \[[RFC8446](#RFC8446)] is the most recent version.[¶](#section-1.5-2)

Implementations MAY also support additional transport-layer security mechanisms that meet their security requirements.[¶](#section-1.5-3)

The identification of the TLS versions and algorithms is outside the scope of this specification. Refer to \[[BCP195](#BCP195)] for up to date recommendations on transport layer security, and to the relevant specifications for certificate validation and other security considerations.[¶](#section-1.5-4)

### [1.6.](#section-1.6) [HTTP Redirections](#name-http-redirections)

This specification makes extensive use of HTTP redirections, in which the client or the authorization server directs the resource owner's user agent to another destination. While the examples in this specification show the use of the HTTP 302 status code, any other method available via the user agent to accomplish this redirection, with the exception of HTTP 307, is allowed and is considered to be an implementation detail. See [Section 7.5.3](#redirect_307) for details.[¶](#section-1.6-1)

### [1.7.](#section-1.7) [Interoperability](#name-interoperability)

OAuth 2.1 provides a rich authorization framework with well-defined security properties.[¶](#section-1.7-1)

This specification leaves a few required components partially or fully undefined (e.g., client registration, authorization server capabilities, endpoint discovery). Some of these behaviors are defined in optional extensions which implementations can choose to use, such as:[¶](#section-1.7-2)

- \[[RFC8414](#RFC8414)]: Authorization Server Metadata, defining an endpoint clients can use to look up the information needed to interact with a particular OAuth server[¶](#section-1.7-3.1.1)
- \[[RFC7591](#RFC7591)]: Dynamic Client Registration, providing a mechanism for programmatically registering clients with an authorization server[¶](#section-1.7-3.2.1)
- \[[RFC7592](#RFC7592)]: Dynamic Client Management, providing a mechanism for updating dynamically registered client information[¶](#section-1.7-3.3.1)
- \[[RFC7662](#RFC7662)]: Token Introspection, defining a mechanism for resource servers to obtain information about access tokens[¶](#section-1.7-3.4.1)

Please refer to [Appendix D](#extensions) for a list of current known extensions at the time of this publication.[¶](#section-1.7-4)

### [1.8.](#section-1.8) [Compatibility with OAuth 2.0](#name-compatibility-with-oauth-20)

OAuth 2.1 is compatible with OAuth 2.0 with the extensions and restrictions from known best current practices applied. Specifically, features not specified in OAuth 2.0 core, such as PKCE, are required in OAuth 2.1. Additionally, some features available in OAuth 2.0, such as the Implicit or Resource Owner Credentials grant types, are not specified in OAuth 2.1. Furthermore, some behaviors allowed in OAuth 2.0 are restricted in OAuth 2.1, such as the strict string matching of redirect URIs required by OAuth 2.1.[¶](#section-1.8-1)

See [Section 10](#oauth-2-0-differences) for more details on the differences from OAuth 2.0.[¶](#section-1.8-2)

### [1.9.](#section-1.9) [Notational Conventions](#name-notational-conventions)

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in BCP 14 \[[RFC2119](#RFC2119)] \[[RFC8174](#RFC8174)] when, and only when, they appear in all capitals, as shown here.[¶](#section-1.9-1)

This specification uses the Augmented Backus-Naur Form (ABNF) notation of \[[RFC5234](#RFC5234)]. Additionally, the rule URI-reference is included from "Uniform Resource Identifier (URI): Generic Syntax" \[[RFC3986](#RFC3986)].[¶](#section-1.9-2)

Certain security-related terms are to be understood in the sense defined in \[[RFC4949](#RFC4949)]. These terms include, but are not limited to, "attack", "authentication", "authorization", "certificate", "confidentiality", "credential", "encryption", "identity", "sign", "signature", "trust", "validate", and "verify".[¶](#section-1.9-3)

The term "content" is to be interpreted as described in [Section 6.4](https://rfc-editor.org/rfc/rfc9110#section-6.4) of \[[RFC9110](#RFC9110)].[¶](#section-1.9-4)

The term "user agent" is to be interpreted as described in [Section 3.5](https://rfc-editor.org/rfc/rfc9110#section-3.5) of \[[RFC9110](#RFC9110)].[¶](#section-1.9-5)

Unless otherwise noted, all the protocol parameter names and values are case sensitive.[¶](#section-1.9-6)

## [2.](#section-2) [Client Registration](#name-client-registration)

Before initiating the protocol, the client must have established an identifier ([Section 2.2](#client-identifier)) at the authorization server. The means through which the client identifier is established with the authorization server are beyond the scope of this specification, but typically involve the client developer manually registering the client at the authorization server's website (after creating an account and agreeing to the service's Terms of Service), or by using Dynamic Client Registration \[[RFC7591](#RFC7591)]. Extensions may also define other programmatic methods of establishing client registration.[¶](#section-2-1)

Client registration does not require a direct interaction between the client and the authorization server. When supported by the authorization server, registration can rely on other means for establishing trust and obtaining the required client properties (e.g., redirect URI, client type). For example, registration can be accomplished using a self-issued or third-party-issued assertion, or by the authorization server performing client discovery using a trusted channel.[¶](#section-2-2)

Client registration MUST include:[¶](#section-2-3)

- the client type as described in [Section 2.1](#client-types),[¶](#section-2-4.1.1)
- client details needed by the grant type in use, such as redirect URIs as described in [Section 2.3](#redirection-endpoint), and[¶](#section-2-4.2.1)
- any other information required by the authorization server (e.g., application name, website, description, logo image, the acceptance of legal terms).[¶](#section-2-4.3.1)

Dynamic Client Registration \[[RFC7591](#RFC7591)] defines a common general data model for clients that may be used even with manual client registration.[¶](#section-2-5)

### [2.1.](#section-2.1) [Client Types](#name-client-types)

OAuth 2.1 defines two client types based on their ability to authenticate securely with the authorization server.[¶](#section-2.1-1)

"confidential":

Clients that have credentials with the AS are designated as "confidential clients"[¶](#section-2.1-2.2.1)

"public":

Clients without credentials are called "public clients"[¶](#section-2.1-2.4.1)

Any clients with credentials MUST take precautions to prevent leakage and abuse of their credentials.[¶](#section-2.1-3)

Client authentication allows an Authorization Server to ensure it is interacting with a certain client (identified by its `client_id`) in an OAuth flow. The Authorization Server might make policy decisions about things such as whether to prompt the user for consent on every authorization or only the first based on the confidence that the Authorization Server is actually communicating with the legitimate client.[¶](#section-2.1-4)

Whether and how an Authorization Server validates the identity of a client or the party providing/operating this client is out of scope of this specification. Authorization servers SHOULD consider the level of confidence in a client's identity when deciding whether they allow a client access to more sensitive resources and operations such as the Client Credentials grant type and how often to prompt the user for consent.[¶](#section-2.1-5)

There is no requirement that an Authorization Server supports a particular client type.[¶](#section-2.1-6)

A single `client_id` SHOULD NOT be treated as more than one type of client.[¶](#section-2.1-7)

This specification has been designed around the following client profiles:[¶](#section-2.1-8)

"web application":

A web application is a client running on a web server. Resource owners access the client via an HTML user interface rendered in a user agent on the device used by the resource owner. The client credentials as well as any access tokens issued to the client are stored on the web server and are not exposed to or accessible by the resource owner.[¶](#section-2.1-9.2.1)

"browser-based application":

A browser-based application is a client in which the client code is downloaded from a web server and executes within a user agent (e.g., web browser) on the device used by the resource owner. Protocol data and credentials are easily accessible (and often visible) to the resource owner. If such applications wish to use client credentials, it is recommended to utilize the backend for frontend pattern. Since such applications reside within the user agent, they can make seamless use of the user agent capabilities when requesting authorization.[¶](#section-2.1-9.4.1)

"native application":

A native application is a client installed and executed on the device used by the resource owner. Protocol data and credentials are accessible to the resource owner. It is assumed that any client authentication credentials included in the application can be extracted. Dynamically issued access tokens and refresh tokens can receive an acceptable level of protection. On some platforms, these credentials are protected from other applications residing on the same device. If such applications wish to use client credentials, it is recommended to utilize the backend for frontend pattern, or issue the credentials at runtime using Dynamic Client Registration \[[RFC7591](#RFC7591)].[¶](#section-2.1-9.6.1)

### [2.2.](#section-2.2) [Client Identifier](#name-client-identifier)

Every client is identified in the context of an authorization server by a client identifier -- a unique string representing the registration information provided by the client. While the Authorization Server typically issues the client identifier itself, it may also serve clients whose client identifier was created by a party other than the Authorization Server. The client identifier is not a secret; it is exposed to the resource owner and MUST NOT be used alone for client authentication. The client identifier is unique in the context of an authorization server.[¶](#section-2.2-1)

The client identifier is an opaque string whose size is left undefined by this specification. The client should avoid making assumptions about the identifier size. The authorization server SHOULD document the size of any identifier it issues.[¶](#section-2.2-2)

If the authorization server supports clients with client identifiers issued by parties other than the authorization server, the authorization server SHOULD take precautions to avoid clients impersonating resource owners as described in [Section 7.4](#client-impersonating-resource-owner).[¶](#section-2.2-3)

### [2.3.](#section-2.3) [Client Redirection Endpoint](#name-client-redirection-endpoint)

The client redirection endpoint (also referred to as "redirect endpoint") is the URI of the client that the authorization server redirects the user agent back to after completing its interaction with the resource owner.[¶](#section-2.3-1)

The authorization server redirects the user agent to one of the client's redirection endpoints previously established with the authorization server during the client registration process.[¶](#section-2.3-2)

The redirect URI MUST be an absolute URI as defined by [Section 4.3](https://rfc-editor.org/rfc/rfc3986#section-4.3) of \[[RFC3986](#RFC3986)]. The redirect URI MAY include an query string component ([Appendix C.1](#query-string-serialization)), which MUST be retained when adding additional query parameters. The redirect URI MUST NOT include a fragment component.[¶](#section-2.3-3)

#### [2.3.1.](#section-2.3.1) [Registration Requirements](#name-registration-requirements)

Authorization servers MUST require clients to register their complete redirect URI (including the path component). Authorization servers MUST reject authorization requests that specify a redirect URI that doesn't exactly match one that was registered, with an exception for loopback redirects, where an exact match is required except for the port URI component, see [Section 4.1.1](#authorization-request) for details.[¶](#section-2.3.1-1)

The authorization server MAY allow the client to register multiple redirect URIs.[¶](#section-2.3.1-2)

Registration may happen out of band, such as a manual step of configuring the client information at the authorization server, or may happen at runtime, such as in the initial POST in Pushed Authorization Requests \[[RFC9126](#RFC9126)].[¶](#section-2.3.1-3)

For private-use URI scheme-based redirect URIs, authorization servers SHOULD enforce the requirement in [Section 8.4.3](#private-use-uri-scheme) that clients use schemes that are reverse domain name based. At a minimum, any private-use URI scheme that doesn't contain a period character (`.`) SHOULD be rejected.[¶](#section-2.3.1-4)

In addition to the collision-resistant properties, this can help to prove ownership in the event of a dispute where two apps claim the same private-use URI scheme (where one app is acting maliciously). For example, if two apps claimed `com.example.app`, the owner of `example.com` could petition the app store operator to remove the counterfeit app. Such a petition is harder to prove if a generic URI scheme was used.[¶](#section-2.3.1-5)

Clients MUST NOT expose URLs that forward the user's browser to arbitrary URIs obtained from a query parameter ("open redirector"), as described in [Section 7.12](#open-redirectors). Open redirectors can enable exfiltration of authorization codes and access tokens.[¶](#section-2.3.1-6)

The client MAY use the `state` request parameter to achieve per-request customization if needed rather than varying the redirect URI per request.[¶](#section-2.3.1-7)

Without requiring registration of redirect URIs, attackers can use the authorization endpoint as an open redirector as described in [Section 7.12](#open-redirectors).[¶](#section-2.3.1-8)

#### [2.3.2.](#section-2.3.2) [Multiple Redirect URIs](#name-multiple-redirect-uris)

If multiple redirect URIs have been registered to a client, the client MUST include a redirect URI with the authorization request using the `redirect_uri` request parameter ([Section 4.1.1](#authorization-request)). If only a single redirect URI has been registered to a client, the `redirect_uri` request parameter is optional.[¶](#section-2.3.2-1)

#### [2.3.3.](#section-2.3.3) [Preventing CSRF Attacks](#name-preventing-csrf-attacks)

Clients MUST prevent Cross-Site Request Forgery (CSRF) attacks. In this context, CSRF refers to requests to the redirection endpoint that do not originate at the authorization server, but a malicious third party (see [Section 4.4.1.8](https://rfc-editor.org/rfc/rfc6819#section-4.4.1.8) of \[[RFC6819](#RFC6819)] for details). Clients that have ensured that the authorization server supports the `code_challenge` parameter MAY rely on the CSRF protection provided by that mechanism. In OpenID Connect flows, validating the `nonce` parameter provides CSRF protection. Otherwise, one-time use CSRF tokens carried in the `state` parameter that are securely bound to the user agent MUST be used for CSRF protection (see [Section 7.9](#csrf_countermeasures)).[¶](#section-2.3.3-1)

#### [2.3.4.](#section-2.3.4) [Preventing Mix-Up Attacks](#name-preventing-mix-up-attacks)

When an OAuth client can only interact with one authorization server, a mix-up defense is not required. In scenarios where an OAuth client interacts with two or more authorization servers, however, clients MUST prevent mix-up attacks. In order to prevent mix-up attacks, clients MUST only process redirect responses of the issuer they sent the respective request to and from the same user agent this authorization request was initiated with.[¶](#section-2.3.4-1)

See [Section 7.14](#mix-up) for a detailed description of two different defenses against mix-up attacks.[¶](#section-2.3.4-2)

#### [2.3.5.](#section-2.3.5) [Invalid Endpoint](#name-invalid-endpoint)

If an authorization request fails validation due to a missing, invalid, or mismatching redirect URI, the authorization server SHOULD inform the resource owner of the error and MUST NOT automatically redirect the user agent to the invalid redirect URI.[¶](#section-2.3.5-1)

#### [2.3.6.](#section-2.3.6) [Endpoint Content](#name-endpoint-content)

The redirection request to the client's endpoint typically results in an HTML document response, processed by the user agent. If the HTML response is served directly as the result of the redirection request, any script included in the HTML document will execute with full access to the redirect URI and the artifacts (e.g., authorization code) it contains. Additionally, the request URL containing the authorization code may be sent in the HTTP Referer header to any embedded images, stylesheets and other elements loaded in the page.[¶](#section-2.3.6-1)

The client SHOULD NOT include any third-party scripts (e.g., third- party analytics, social plug-ins, ad networks) in the redirect URI endpoint response. Instead, it SHOULD extract the artifacts from the URI and redirect the user agent again to another endpoint without exposing the artifacts (in the URI or elsewhere). If third-party scripts are included, the client MUST ensure that its own scripts (used to extract and remove the credentials from the URI) will execute first.[¶](#section-2.3.6-2)

### [2.4.](#section-2.4) [Client Authentication](#name-client-authentication)

The authorization server MUST only rely on client authentication if the process of issuance/registration and distribution of the underlying credentials ensures their confidentiality.[¶](#section-2.4-1)

For confidential clients, the authorization server MAY accept any form of client authentication meeting its security requirements (e.g., client secret, public/private key pair).[¶](#section-2.4-2)

It is RECOMMENDED to use asymmetric (public-key based) methods for client authentication such as mTLS \[[RFC8705](#RFC8705)] or using signed JWTs ("Private Key JWT") in accordance with \[[RFC7521](#RFC7521)], \[[RFC7523](#RFC7523)], and their update \[[I-D.ietf-oauth-rfc7523bis](#I-D.ietf-oauth-rfc7523bis)] (defined in \[[OpenID.Connect](#OpenID.Connect)] as the client authentication method `private_key_jwt`). When such methods for client authentication are used, authorization servers do not need to store sensitive symmetric keys, making these methods more robust against a number of attacks, and enables clients to manage their own keys and key rotation.[¶](#section-2.4-3)

When using JWT-based client authentication, clients and authorization servers MUST follow the updated guidance around `aud` values in \[[I-D.ietf-oauth-rfc7523bis](#I-D.ietf-oauth-rfc7523bis)].[¶](#section-2.4-4)

When client authentication is not possible, the authorization server SHOULD employ other means to validate the client's identity -- for example, by requiring the registration of the client redirect URI or enlisting the resource owner to confirm identity. A valid redirect URI is not sufficient to verify the client's identity when asking for resource owner authorization but can be used to prevent delivering credentials to a counterfeit client after obtaining resource owner authorization.[¶](#section-2.4-5)

The client MUST NOT use more than one authentication method in each request to prevent a conflict of which authentication mechanism is authoritative for the request.[¶](#section-2.4-6)

The authorization server MUST consider the security implications of interacting with unauthenticated clients and take measures to limit the potential exposure of tokens issued to such clients, (e.g., limiting the lifetime of refresh tokens).[¶](#section-2.4-7)

The privileges an authorization server associates with a certain client identity MUST depend on the assessment of the overall process for client identification and client credential lifecycle management. See [Section 7.2](#security-client-authentication) for additional details.[¶](#section-2.4-8)

#### [2.4.1.](#section-2.4.1) [Client Secret](#name-client-secret)

To support confidential clients in possession of a client secret, the authorization server MUST support the client including the client credentials in the request body content using the following parameters:[¶](#section-2.4.1-1)

"client\_id":

REQUIRED. The client identifier issued to the client during the registration process described by [Section 2.2](#client-identifier).[¶](#section-2.4.1-2.2.1)

"client\_secret":

REQUIRED. The client secret.[¶](#section-2.4.1-2.4.1)

The parameters can only be transmitted in the request content and MUST NOT be included in the request URI.[¶](#section-2.4.1-3)

This is also known as `client_secret_post` as defined in [Section 2](https://rfc-editor.org/rfc/rfc7591#section-2) of \[[RFC7591](#RFC7591)].[¶](#section-2.4.1-4)

For example, a request to refresh an access token ([Section 4.3](#refreshing-an-access-token)) using the content parameters (with extra line breaks for display purposes only):[¶](#section-2.4.1-5)

```
POST /token HTTP/1.1
Host: server.example.com
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token&refresh_token=tGzv3JOkF0XG5Qx2TlKWIA
&client_id=s6BhdRkqt3&client_secret=7Fjfp0ZBr1KtDRbnfVdmIw
```

[¶](#section-2.4.1-6)

The authorization server MAY support the HTTP Basic authentication scheme for authenticating clients that were issued a client secret.[¶](#section-2.4.1-7)

When using the HTTP Basic authentication scheme as defined in [Section 11](https://rfc-editor.org/rfc/rfc9110#section-11) of \[[RFC9110](#RFC9110)] to authenticate with the authorization server, the client identifier is encoded using the `application/x-www-form-urlencoded` encoding algorithm per [Appendix B](#application-x-www-form-urlencoded), and the encoded value is used as the username; the client secret is encoded using the same algorithm and used as the password.[¶](#section-2.4.1-8)

This is also known as `client_secret_basic` as defined in [Section 2](https://rfc-editor.org/rfc/rfc7591#section-2) of \[[RFC7591](#RFC7591)].[¶](#section-2.4.1-9)

For example (with extra line breaks for display purposes only):[¶](#section-2.4.1-10)

```
Authorization: Basic czZCaGRSa3F0Mzo3RmpmcDBaQnIxS3REUmJuZlZkbUl3
```

[¶](#section-2.4.1-11)

Note: This method of initially form-encoding the client identifier and secret, and then using the encoded values as the HTTP Basic authentication username and password, has led to many interoperability problems in the past. Some implementations have missed the encoding step, or decided to only encode certain characters, or ignored the encoding requirement when validating the credentials, leading to clients having to special-case how they present the credentials to individual authorization servers. Including the credentials in the request body content avoids the encoding issues and leads to more interoperable implementations.[¶](#section-2.4.1-12)

Since the client secret authentication method involves a password, the authorization server MUST protect any endpoint utilizing it against brute force attacks.[¶](#section-2.4.1-13)

#### [2.4.2.](#section-2.4.2) [Other Authentication Methods](#name-other-authentication-method)

The authorization server MAY support any suitable authentication scheme matching its security requirements. When using other authentication methods, the authorization server MUST define a mapping between the client identifier (registration record) and authentication scheme.[¶](#section-2.4.2-1)

Some additional authentication methods such as mTLS \[[RFC8705](#RFC8705)] and Private Key JWT (\[[RFC7523](#RFC7523)], \[[I-D.ietf-oauth-rfc7523bis](#I-D.ietf-oauth-rfc7523bis)]) are defined in the "[OAuth Token Endpoint Authentication Methods](https://www.iana.org/assignments/oauth-parameters/oauth-parameters.xhtml#token-endpoint-auth-method)" registry, and may be useful as generic client authentication methods beyond the specific use of protecting the token endpoint.[¶](#section-2.4.2-2)

### [2.5.](#section-2.5) [Unregistered Clients](#name-unregistered-clients)

This specification does not require that clients be registered with the authorization server. However, the use of unregistered clients is beyond the scope of this specification and requires additional security analysis and review of its interoperability impact.[¶](#section-2.5-1)

## [3.](#section-3) [Protocol Endpoints](#name-protocol-endpoints)

The authorization process utilizes two authorization server endpoints (HTTP resources):[¶](#section-3-1)

- Authorization endpoint - used by the client to obtain authorization from the resource owner via user agent redirection.[¶](#section-3-2.1.1)
- Token endpoint - used by the client to exchange an authorization grant for an access token, typically with client authentication.[¶](#section-3-2.2.1)

As well as one client endpoint:[¶](#section-3-3)

- Redirection endpoint - used by the authorization server to return responses containing authorization credentials to the client via the resource owner user agent.[¶](#section-3-4.1.1)

Not every authorization grant type utilizes both endpoints. Extension grant types MAY define additional endpoints as needed.[¶](#section-3-5)

### [3.1.](#section-3.1) [Authorization Endpoint](#name-authorization-endpoint)

The authorization endpoint is used to interact with the resource owner and obtain an authorization grant. The authorization server MUST first authenticate the resource owner. The way in which the authorization server authenticates the resource owner (e.g., username and password login, passkey, federated login, or by using an established session) is beyond the scope of this specification.[¶](#section-3.1-1)

The means through which the client obtains the URL of the authorization endpoint are beyond the scope of this specification, but the URL is typically provided in the service documentation, or in the authorization server's metadata document \[[RFC8414](#RFC8414)].[¶](#section-3.1-2)

The authorization endpoint URL MUST NOT include a fragment component, and MAY include a query string component [Appendix C.1](#query-string-serialization), which MUST be retained when adding additional query parameters.[¶](#section-3.1-3)

The authorization server MUST support the use of the HTTP `GET` method [Section 9.3.1](https://rfc-editor.org/rfc/rfc9110#section-9.3.1) of \[[RFC9110](#RFC9110)] for the authorization endpoint and MAY support the `POST` method ([Section 9.3.3](https://rfc-editor.org/rfc/rfc9110#section-9.3.3) of \[[RFC9110](#RFC9110)]) as well.[¶](#section-3.1-4)

The authorization server MUST ignore unrecognized request parameters sent to the authorization endpoint.[¶](#section-3.1-5)

Request and response parameters defined by this specification MUST NOT be included more than once. This requirement also applies to parameters defined by extensions unless the extension explicitly defines otherwise for a specific parameter. Parameters sent without a value MUST be treated as if they were omitted from the request.[¶](#section-3.1-6)

An authorization server that redirects a request potentially containing user credentials MUST avoid forwarding these user credentials accidentally (see [Section 7.5.3](#redirect_307) for details).[¶](#section-3.1-7)

Cross-Origin Resource Sharing \[[WHATWG.CORS](#WHATWG.CORS)] MUST NOT be supported at the Authorization Endpoint as the client does not access this endpoint directly, instead the client redirects the user agent to it.[¶](#section-3.1-8)

### [3.2.](#section-3.2) [Token Endpoint](#name-token-endpoint)

The token endpoint is used by the client to obtain an access token using a grant such as those described in [Section 4](#obtaining-authorization) and [Section 4.3](#refreshing-an-access-token).[¶](#section-3.2-1)

The means through which the client obtains the URL of the token endpoint are beyond the scope of this specification, but the URL is typically provided in the service documentation and configured during development of the client, or provided in the authorization server's metadata document \[[RFC8414](#RFC8414)] and fetched programmatically at runtime.[¶](#section-3.2-2)

The token endpoint URL MUST NOT include a fragment component, and MAY include a query string component [Appendix C.1](#query-string-serialization).[¶](#section-3.2-3)

The client MUST use the HTTP `POST` method when making requests to the token endpoint.[¶](#section-3.2-4)

The authorization server MUST ignore unrecognized request parameters sent to the token endpoint.[¶](#section-3.2-5)

Parameters sent without a value MUST be treated as if they were omitted from the request. Request and response parameters defined by this specification MUST NOT be included more than once. This requirement also applies to parameters defined by extensions unless the extension explicitly defines otherwise for a specific parameter.[¶](#section-3.2-6)

Authorization servers that wish to support browser-based applications (for example, applications running exclusively in client-side JavaScript without access to a supporting backend server) will need to ensure the token endpoint supports the necessary CORS \[[WHATWG.CORS](#WHATWG.CORS)] headers to allow the responses to be visible to the application. If the authorization server provides additional endpoints to the application, such as metadata URLs, dynamic client registration, revocation, introspection, discovery or user info endpoints, these endpoints may also be accessed by the browser-based application, and will also need to have the CORS headers defined to allow access. See \[[I-D.ietf-oauth-browser-based-apps](#I-D.ietf-oauth-browser-based-apps)] for further details.[¶](#section-3.2-7)

#### [3.2.1.](#section-3.2.1) [Client Authentication](#name-client-authentication-2)

Confidential clients MUST authenticate with the authorization server as described in [Section 2.4](#client-authentication) when making requests to the token endpoint.[¶](#section-3.2.1-1)

Client authentication is used for:[¶](#section-3.2.1-2)

- Enforcing the binding of refresh tokens and authorization codes to the client they were issued to. Client authentication adds an additional layer of security when an authorization code is transmitted to the redirection endpoint over an insecure channel.[¶](#section-3.2.1-3.1.1)
- Recovering from a compromised client by disabling the client or changing its credentials, thus preventing an attacker from abusing stolen refresh tokens. Changing a single set of client credentials is significantly faster than revoking an entire set of refresh tokens.[¶](#section-3.2.1-3.2.1)
- Implementing authentication management best practices, which require periodic credential rotation. Rotation of an entire set of refresh tokens can be challenging, while rotation of a single set of client credentials is significantly easier.[¶](#section-3.2.1-3.3.1)

#### [3.2.2.](#section-3.2.2) [Token Endpoint Request](#name-token-endpoint-request)

The client makes a request to the token endpoint by sending the following parameters using the form-encoded serialization format per [Appendix C.2](#form-serialization) with a character encoding of UTF-8 in the HTTP request content:[¶](#section-3.2.2-1)

"grant\_type":

REQUIRED. Identifier of the grant type the client uses with the particular token request. This specification defines the values `authorization_code`, `refresh_token`, and `client_credentials`. The grant type determines the further parameters required or supported by the token request. The details of those grant types are defined below.[¶](#section-3.2.2-2.2.1)

"client\_id":

OPTIONAL. The client identifier is needed when a form of client authentication that relies on the parameter is used, or the `grant_type` requires identification of public clients.[¶](#section-3.2.2-2.4.1)

Confidential clients MUST authenticate with the authorization server as described in [Section 3.2.1](#token-endpoint-client-authentication).[¶](#section-3.2.2-3)

For example, the client makes the following HTTPS request (with extra line breaks for display purposes only):[¶](#section-3.2.2-4)

```
POST /token HTTP/1.1
Host: server.example.com
Authorization: Basic czZCaGRSa3F0MzpnWDFmQmF0M2JW
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code
&code=SplxlOBeZQQYbYS6WxSbIA
&redirect_uri=https%3A%2F%2Fclient%2Eexample%2Ecom%2Fcb
&code_verifier=3641a2d12d66101249cdf7a79c000c1f8c05d2aafcf14bf146497bed
```

[¶](#section-3.2.2-5)

The authorization server MUST:[¶](#section-3.2.2-6)

- require client authentication for confidential clients (or clients with other authentication requirements),[¶](#section-3.2.2-7.1.1)
- authenticate the client if client authentication is included[¶](#section-3.2.2-7.2.1)

Further grant type specific processing rules apply and are specified with the respective grant type.[¶](#section-3.2.2-8)

#### [3.2.3.](#section-3.2.3) [Token Endpoint Response](#name-token-endpoint-response)

If the access token request is valid and authorized, the authorization server issues an access token and optional refresh token.[¶](#section-3.2.3-1)

If the client authentication failed or is invalid, the authorization server returns an error response as described in [Section 3.2.4](#token-error-response).[¶](#section-3.2.3-2)

The authorization server issues an access token and optional refresh token by creating an HTTP response according to [Appendix C.3](#json-serialization), using the `application/json` media type as defined by \[[RFC8259](#RFC8259)], with the following parameters and an HTTP 200 (OK) status code:[¶](#section-3.2.3-3)

"access\_token":

REQUIRED. The access token issued by the authorization server.[¶](#section-3.2.3-4.2.1)

"token\_type":

REQUIRED. The type of the access token issued as described in [Section 1.4](#access-tokens). Value is case insensitive.[¶](#section-3.2.3-4.4.1)

"expires\_in":

RECOMMENDED. A JSON number that represents the lifetime in seconds of the access token. For example, the value `3600` denotes that the access token will expire in one hour from the time the response was generated. If omitted, the authorization server SHOULD provide the lifetime via other means or document the default value. Note that the authorization server may prematurely expire an access token and clients MUST NOT expect an access token to be valid for the provided lifetime.[¶](#section-3.2.3-4.6.1)

"scope":

RECOMMENDED, if identical to the scope requested by the client; otherwise, REQUIRED. The scope of the access token as described by [Section 1.4.1](#access-token-scope).[¶](#section-3.2.3-4.8.1)

"refresh\_token":

OPTIONAL. The refresh token, which can be used to obtain new access tokens based on the grant passed in the corresponding token request.[¶](#section-3.2.3-4.10.1)

Authorization servers SHOULD determine, based on a risk assessment and their own policies, whether to issue refresh tokens to a certain client. If the authorization server decides not to issue refresh tokens, the client MAY obtain new access tokens by starting the OAuth flow over, for example initiating a new authorization code request. In such a case, the authorization server may utilize cookies and persistent grants to optimize the user experience.[¶](#section-3.2.3-5)

If refresh tokens are issued, those refresh tokens MUST be bound to the scope and resource servers as consented by the resource owner. This is to prevent privilege escalation by the legitimate client and reduce the impact of refresh token leakage.[¶](#section-3.2.3-6)

The parameters are serialized into a JavaScript Object Notation (JSON) structure as described in [Appendix C.3](#json-serialization).[¶](#section-3.2.3-7)

The authorization server MUST include the HTTP `Cache-Control` response header field (see [Section 5.2](https://rfc-editor.org/rfc/rfc9111#section-5.2) of \[[RFC9111](#RFC9111)]) with a value of `no-store` in any response containing tokens, credentials, or other sensitive information.[¶](#section-3.2.3-8)

For example:[¶](#section-3.2.3-9)

```
HTTP/1.1 200 OK
Content-Type: application/json
Cache-Control: no-store

{
  "access_token": "2YotnFZFEjr1zCsicMWpAA",
  "token_type": "Bearer",
  "expires_in": 3600,
  "refresh_token": "tGzv3JOkF0XG5Qx2TlKWIA",
  "example_parameter": "example_value"
}
```

[¶](#section-3.2.3-10)

The client MUST ignore unrecognized value names in the response. The sizes of tokens and other values received from the authorization server are left undefined. The client should avoid making assumptions about value sizes. The authorization server SHOULD document the size of any value it issues.[¶](#section-3.2.3-11)

#### [3.2.4.](#section-3.2.4) [Token Endpoint Error Response](#name-token-endpoint-error-respon)

The authorization server responds with an HTTP 400 (Bad Request) status code (unless specified otherwise) and includes the following parameters with the response:[¶](#section-3.2.4-1)

"error":

REQUIRED. A single ASCII \[[USASCII](#USASCII)] error code from the following:[¶](#section-3.2.4-2.2.1)

"invalid\_request":

The request is missing a required parameter, includes an unsupported parameter value (other than grant type), repeats a parameter, includes multiple credentials, utilizes more than one mechanism for authenticating the client, contains a `code_verifier` although no `code_challenge` was sent in the authorization request, or is otherwise malformed.[¶](#section-3.2.4-2.2.2.2.1)

"invalid\_client":

Client authentication failed (e.g., unknown client, no client authentication included, or unsupported authentication method). The authorization server MAY return an HTTP 401 (Unauthorized) status code to indicate which HTTP authentication schemes are supported. If the client attempted to authenticate via the `Authorization` request header field, the authorization server MUST respond with an HTTP 401 (Unauthorized) status code and include the `WWW-Authenticate` response header field matching the authentication scheme used by the client.[¶](#section-3.2.4-2.2.2.4.1)

"invalid\_grant":

The provided authorization grant (e.g., authorization code, resource owner credentials) or refresh token is invalid, expired, revoked, does not match the redirect URI used in the authorization request, or was issued to another client.[¶](#section-3.2.4-2.2.2.6.1)

"unauthorized\_client":

The authenticated client is not authorized to use this authorization grant type.[¶](#section-3.2.4-2.2.2.8.1)

"unsupported\_grant\_type":

The authorization grant type is not supported by the authorization server.[¶](#section-3.2.4-2.2.2.10.1)

"invalid\_scope":

The requested scope is invalid, unknown, malformed, or exceeds the scope granted by the resource owner.[¶](#section-3.2.4-2.2.2.12.1)

Values for the `error` parameter MUST NOT include characters outside the set %x20-21 / %x23-5B / %x5D-7E.[¶](#section-3.2.4-2.2.3)

"error\_description":

OPTIONAL. Human-readable ASCII \[[USASCII](#USASCII)] text providing additional information, used to assist the client developer in understanding the error that occurred. Values for the `error_description` parameter MUST NOT include characters outside the set %x20-21 / %x23-5B / %x5D-7E.[¶](#section-3.2.4-2.4.1)

"error\_uri":

OPTIONAL. A URI identifying a human-readable web page with information about the error, used to provide the client developer with additional information about the error. Values for the `error_uri` parameter MUST conform to the URI-reference syntax and thus MUST NOT include characters outside the set %x21 / %x23-5B / %x5D-7E.[¶](#section-3.2.4-2.6.1)

The parameters are included in the content of the HTTP response using the `application/json` media type as defined in [Appendix C.3](#json-serialization).[¶](#section-3.2.4-3)

For example:[¶](#section-3.2.4-4)

```
HTTP/1.1 400 Bad Request
Content-Type: application/json
Cache-Control: no-store

{
 "error": "invalid_request"
}
```

[¶](#section-3.2.4-5)

## [4.](#section-4) [Grant Types](#name-grant-types)

To request an access token, the client obtains authorization from the resource owner. This specification defines the following authorization grant types:[¶](#section-4-1)

- authorization code[¶](#section-4-2.1.1)
- client credentials, and[¶](#section-4-2.2.1)
- refresh token[¶](#section-4-2.3.1)

It also provides an extension mechanism for defining additional grant types.[¶](#section-4-3)

### [4.1.](#section-4.1) [Authorization Code Grant](#name-authorization-code-grant)

The authorization code grant type is used to obtain both access tokens and refresh tokens.[¶](#section-4.1-1)

The grant type uses the additional authorization endpoint to let the authorization server interact with the resource owner in order to get consent for resource access.[¶](#section-4.1-2)

Since this is a redirect-based flow, the client must be capable of initiating the flow with the resource owner's user agent (typically a web browser) and capable of being redirected back to from the authorization server.[¶](#section-4.1-3)

```
 +----------+
 | Resource |
 |   Owner  |
 +----------+
       ^
       |
       |
 +-----|----+          Client Identifier      +---------------+
 | .---+---------(1)-- & Redirect URI ------->|               |
 | |   |    |                                 |               |
 | |   '---------(2)-- User authenticates --->|               |
 | | User-  |                                 | Authorization |
 | | Agent  |                                 |     Server    |
 | |        |                                 |               |
 | |    .--------(3)-- Authorization Code ---<|               |
 +-|----|---+                                 +---------------+
   |    |                                         ^      v
   |    |                                         |      |
   ^    v                                         |      |
 +---------+                                      |      |
 |         |>---(4)-- Authorization Code ---------'      |
 |  Client |          & Redirect URI                     |
 |         |                                             |
 |         |<---(5)----- Access Token -------------------'
 +---------+       (w/ Optional Refresh Token)
```

[Figure 3](#figure-3): [Authorization Code Flow](#name-authorization-code-flow)

The flow illustrated in [Figure 3](#fig-authorization-code-flow) includes the following steps:[¶](#section-4.1-5)

(1) The client initiates the flow by directing the resource owner's user agent to the authorization endpoint. The client includes its client identifier, code challenge (derived from a generated code verifier), optional requested scope, optional local state, and a redirect URI to which the authorization server will send the user agent back once access is granted (or denied).[¶](#section-4.1-6)

(2) The authorization server authenticates the resource owner (via the user agent) and establishes whether the resource owner grants or denies the client's access request.[¶](#section-4.1-7)

(3) Assuming the resource owner grants access, the authorization server redirects the user agent back to the client using the redirect URI provided earlier (in the request or during client registration). The redirect URI includes an authorization code and any local state provided by the client earlier.[¶](#section-4.1-8)

(4) The client requests an access token from the authorization server's token endpoint by including the authorization code received in the previous step, and including its code verifier. When making the request, the client authenticates with the authorization server if it can. The client includes the redirect URI used to obtain the authorization code for verification.[¶](#section-4.1-9)

(5) The authorization server authenticates the client when possible, validates the authorization code, validates the code verifier, and ensures that the redirect URI received matches the URI used to redirect the user agent to the client in step (3). If valid, the authorization server responds back with an access token and, optionally, a refresh token.[¶](#section-4.1-10)

#### [4.1.1.](#section-4.1.1) [Authorization Request](#name-authorization-request)

To begin the authorization request, the client builds the authorization request URI by adding parameters to the authorization server's authorization endpoint URI. The client will eventually redirect the user agent to this URI to initiate the request.[¶](#section-4.1.1-1)

Clients use a unique secret, called the "code verifier", per authorization request to protect against authorization code injection and CSRF attacks. The client first generates the code verifier, then derives the "code challenge" to include in the authorization request. The client uses the code verifier when exchanging the authorization code at the token endpoint to prove that the client using the authorization code is the same client that requested it.[¶](#section-4.1.1-2)

The client constructs the request URI by adding the following parameters to the query component of the authorization endpoint URI as described by [Appendix C.1](#query-string-serialization):[¶](#section-4.1.1-3)

"response\_type":

REQUIRED. The authorization endpoint supports different sets of request and response parameters. The client determines the type of flow by using a certain `response_type` value. This specification defines the value `code`, which must be used to signal that the client wants to use the authorization code flow.[¶](#section-4.1.1-4.2.1)

Extension response types MAY contain a space-delimited (%x20) list of values, where the order of values does not matter (e.g., response type `a b` is the same as `b a`). The meaning of such composite response types is defined by their respective specifications.[¶](#section-4.1.1-5)

Some extension response types are defined by \[[OpenID.Connect](#OpenID.Connect)].[¶](#section-4.1.1-6)

If an authorization request is missing the `response_type` parameter, or if the response type is not understood, the authorization server MUST return an error response as described in [Section 4.1.2.1](#authorization-code-error-response).[¶](#section-4.1.1-7)

"client\_id":

REQUIRED. The client identifier as described in [Section 2.2](#client-identifier).[¶](#section-4.1.1-8.2.1)

"code\_challenge":

REQUIRED unless the specific requirements of [Section 7.5.1](#authorization_codes) are met. Code challenge derived from the code verifier.[¶](#section-4.1.1-8.4.1)

"code\_challenge\_method":

OPTIONAL, defaults to `plain` if not present in the request. Code verifier transformation method is `S256` or `plain`.[¶](#section-4.1.1-8.6.1)

"redirect\_uri":

OPTIONAL if only one redirect URI is registered for this client. REQUIRED if multiple redirect URIs are registered for this client. See [Section 2.3.2](#multiple-redirect-uris).[¶](#section-4.1.1-8.8.1)

"scope":

OPTIONAL. The scope of the access request as described by [Section 1.4.1](#access-token-scope).[¶](#section-4.1.1-8.10.1)

"state":

OPTIONAL. An opaque value used by the client to maintain state between the request and callback. The authorization server includes this value when redirecting the user agent back to the client.[¶](#section-4.1.1-8.12.1)

The `code_verifier` is a unique high-entropy cryptographically random string generated for each authorization request, using the unreserved characters `[A-Z] / [a-z] / [0-9] / "-" / "." / "_" / "~"`, with a minimum length of 43 characters and a maximum length of 128 characters.[¶](#section-4.1.1-9)

The client stores the `code_verifier` temporarily, and calculates the `code_challenge` which it uses in the authorization request.[¶](#section-4.1.1-10)

ABNF for `code_verifier` is as follows.[¶](#section-4.1.1-11)

```
code-verifier = 43*128unreserved
unreserved = ALPHA / DIGIT / "-" / "." / "_" / "~"
ALPHA = %x41-5A / %x61-7A
DIGIT = %x30-39
```

[¶](#section-4.1.1-12)

Clients SHOULD use code challenge methods that do not expose the `code_verifier` in the authorization request. Otherwise, attackers that can read the authorization request (cf. Attacker A4 in \[[RFC9700](#RFC9700)]) can break the security provided by this mechanism. Currently, `S256` is the only such method.[¶](#section-4.1.1-13)

NOTE: The code verifier SHOULD have enough entropy to make it impractical to guess the value. It is RECOMMENDED that the output of a suitable random number generator be used to create a 32-octet sequence. The octet sequence is then base64url-encoded to produce a 43-octet URL-safe string to use as the code verifier.[¶](#section-4.1.1-14)

The client then creates a `code_challenge` derived from the code verifier by using one of the following transformations on the code verifier:[¶](#section-4.1.1-15)

```
S256
  code_challenge = BASE64URL-ENCODE(SHA256(ASCII(code_verifier)))

plain
  code_challenge = code_verifier
```

[¶](#section-4.1.1-16)

If the client is capable of using `S256`, it MUST use `S256`, as `S256` is Mandatory To Implement (MTI) on the server. Clients are permitted to use `plain` only if they cannot support `S256` for some technical reason, for example constrained environments that do not have a hashing function available, and know via out-of-band configuration or via Authorization Server Metadata \[[RFC8414](#RFC8414)] that the server supports `plain`.[¶](#section-4.1.1-17)

ABNF for `code_challenge` is as follows.[¶](#section-4.1.1-18)

```
code-challenge = 43*128unreserved
unreserved = ALPHA / DIGIT / "-" / "." / "_" / "~"
ALPHA = %x41-5A / %x61-7A
DIGIT = %x30-39
```

[¶](#section-4.1.1-19)

The properties `code_challenge` and `code_verifier` are adopted from the OAuth 2.0 extension known as "Proof-Key for Code Exchange", or PKCE \[[RFC7636](#RFC7636)] where this technique was originally developed.[¶](#section-4.1.1-20)

Authorization servers MUST support the `code_challenge` and `code_verifier` parameters.[¶](#section-4.1.1-21)

Clients MUST use `code_challenge` and `code_verifier` and authorization servers MUST enforce their use except under the conditions described in [Section 7.5.1](#authorization_codes). Even in this case, using and enforcing `code_challenge` and `code_verifier` as described above is still RECOMMENDED.[¶](#section-4.1.1-22)

The `state` and `scope` parameters SHOULD NOT include sensitive client or resource owner information in plain text, as they can be transmitted over insecure channels or stored insecurely.[¶](#section-4.1.1-23)

The client directs the resource owner to the constructed URI using an HTTP redirection, or by other means available to it via the user agent.[¶](#section-4.1.1-24)

For example, the client directs the user agent to make the following HTTPS request (with extra line breaks for display purposes only):[¶](#section-4.1.1-25)

```
GET /authorize?response_type=code&client_id=s6BhdRkqt3&state=xyz
    &redirect_uri=https%3A%2F%2Fclient%2Eexample%2Ecom%2Fcb
    &code_challenge=6fdkQaPm51l13DSukcAH3Mdx7_ntecHYd1vi3n0hMZY
    &code_challenge_method=S256 HTTP/1.1
Host: server.example.com
```

[¶](#section-4.1.1-26)

The authorization server validates the request to ensure that all required parameters are present and valid.[¶](#section-4.1.1-27)

In particular, the authorization server MUST validate the `redirect_uri` in the request if present, ensuring that it matches one of the registered redirect URIs previously established during client registration ([Section 2](#client-registration)). When comparing the two URIs the authorization server MUST ensure that the two URIs are equal, see [Section 6.2.1](https://rfc-editor.org/rfc/rfc3986#section-6.2.1) of \[[RFC3986](#RFC3986)], Simple String Comparison, for details. The only exception is native apps using a localhost URI: In this case, the authorization server MUST allow variable port numbers as described in [Section 7.3](https://rfc-editor.org/rfc/rfc8252#section-7.3) of \[[RFC8252](#RFC8252)].[¶](#section-4.1.1-28)

If the request is valid, the authorization server authenticates the resource owner and obtains an authorization decision (by asking the resource owner or by establishing approval via other means).[¶](#section-4.1.1-29)

When a decision is established, the authorization server directs the user agent to the provided client redirect URI using an HTTP redirection response, or by other means available to it via the user agent.[¶](#section-4.1.1-30)

#### [4.1.2.](#section-4.1.2) [Authorization Response](#name-authorization-response)

If the resource owner grants the access request, the authorization server issues an authorization code and delivers it to the client by adding the following parameters to the query component of the redirect URI using the query string serialization described by [Appendix C.1](#query-string-serialization), unless specified otherwise by an extension:[¶](#section-4.1.2-1)

"code":

REQUIRED. The authorization code is generated by the authorization server and opaque to the client. The authorization code MUST expire shortly after it is issued to mitigate the risk of leaks. A maximum authorization code lifetime of 10 minutes is RECOMMENDED. The authorization code is bound to the client identifier, code challenge and redirect URI.[¶](#section-4.1.2-2.2.1)

"state":

REQUIRED if the `state` parameter was present in the client authorization request. The exact value received from the client.[¶](#section-4.1.2-2.4.1)

"iss":

OPTIONAL. The identifier of the authorization server which the client can use to prevent mix-up attacks, if the client interacts with more than one authorization server. See [Section 7.14](#mix-up) and \[[RFC9207](#RFC9207)] for additional details on when this parameter is necessary, and how the client can use it to prevent mix-up attacks.[¶](#section-4.1.2-2.6.1)

For example, the authorization server redirects the user agent by sending the following HTTP response:[¶](#section-4.1.2-3)

```
HTTP/1.1 302 Found
Location: https://client.example.com/cb?code=SplxlOBeZQQYbYS6WxSbIA
          &state=xyz&iss=https%3A%2F%2Fauthorization-server.example.com
```

[¶](#section-4.1.2-4)

The client MUST ignore unrecognized response parameters. The authorization code string size is left undefined by this specification. The client should avoid making assumptions about code value sizes. The authorization server SHOULD document the size of any value it issues.[¶](#section-4.1.2-5)

The authorization server MUST associate the `code_challenge` and `code_challenge_method` values with the issued authorization code so the code challenge can be verified later.[¶](#section-4.1.2-6)

The exact method that the server uses to associate the `code_challenge` with the issued code is out of scope for this specification. The code challenge could be stored on the server and associated with the code there. The `code_challenge` and `code_challenge_method` values may be stored in encrypted form in the code itself, but the server MUST NOT include the `code_challenge` value in a response parameter in a form that entities other than the AS can extract.[¶](#section-4.1.2-7)

Clients MUST prevent injection (replay) of authorization codes into the authorization response by attackers. Using `code_challenge` and `code_verifier` prevents injection of authorization codes since the authorization server will reject a token request with a mismatched `code_verifier`. See [Section 7.5.1](#authorization_codes) for more details.[¶](#section-4.1.2-8)

##### [4.1.2.1.](#section-4.1.2.1) [Authorization Error Response](#name-authorization-error-respons)

If the request fails due to a missing, invalid, or mismatching redirect URI, or if the client identifier is missing or invalid, the authorization server MUST NOT redirect the user agent to the invalid redirect URI and SHOULD inform the resource owner of the error, for example by displaying a message to the user in their browser.[¶](#section-4.1.2.1-1)

An authorization server MUST reject requests without a `code_challenge` from public clients, and MUST reject such requests from other clients unless there is reasonable assurance that the client mitigates authorization code injection in other ways. See [Section 7.5.1](#authorization_codes) for details.[¶](#section-4.1.2.1-2)

If the server does not support the requested `code_challenge_method` transformation, the authorization endpoint MUST return the authorization error response with `error` value set to `invalid_request`. The `error_description` or the response of `error_uri` SHOULD explain the nature of error, e.g., transform algorithm not supported.[¶](#section-4.1.2.1-3)

If the resource owner denies the access request or if the request fails for reasons other than a missing or invalid redirect URI, the authorization server informs the client by redirecting the user agent to the redirect URI and adding the following parameters to the query component of the redirect URI as described by [Appendix C.1](#query-string-serialization):[¶](#section-4.1.2.1-4)

"error":

REQUIRED. A single ASCII \[[USASCII](#USASCII)] error code from the following:[¶](#section-4.1.2.1-5.2.1)

"invalid\_request":

The request is missing a required parameter, includes an invalid parameter value, includes a parameter more than once, or is otherwise malformed.[¶](#section-4.1.2.1-5.2.2.2.1)

"unauthorized\_client":

The client is not authorized to request an authorization code using this method.[¶](#section-4.1.2.1-5.2.2.4.1)

"access\_denied":

The resource owner or authorization server denied the request.[¶](#section-4.1.2.1-5.2.2.6.1)

"unsupported\_response\_type":

The authorization server does not support obtaining an authorization code using this method.[¶](#section-4.1.2.1-5.2.2.8.1)

"invalid\_scope":

The requested scope is invalid, unknown, or malformed.[¶](#section-4.1.2.1-5.2.2.10.1)

"server\_error":

The authorization server encountered an unexpected condition that prevented it from fulfilling the request. (This error code is needed because a 500 Internal Server Error HTTP status code cannot be returned to the client via an HTTP redirect.)[¶](#section-4.1.2.1-5.2.2.12.1)

"temporarily\_unavailable":

The authorization server is currently unable to handle the request due to a temporary overloading or maintenance of the server. (This error code is needed because a 503 Service Unavailable HTTP status code cannot be returned to the client via an HTTP redirect.)[¶](#section-4.1.2.1-5.2.2.14.1)

Values for the `error` parameter MUST NOT include characters outside the set %x20-21 / %x23-5B / %x5D-7E.[¶](#section-4.1.2.1-5.2.3)

"error\_description":

OPTIONAL. Human-readable ASCII \[[USASCII](#USASCII)] text providing additional information, used to assist the client developer in understanding the error that occurred. Values for the `error_description` parameter MUST NOT include characters outside the set %x20-21 / %x23-5B / %x5D-7E.[¶](#section-4.1.2.1-5.4.1)

"error\_uri":

OPTIONAL. A URI identifying a human-readable web page with information about the error, used to provide the client developer with additional information about the error. Values for the `error_uri` parameter MUST conform to the URI-reference syntax and thus MUST NOT include characters outside the set %x21 / %x23-5B / %x5D-7E.[¶](#section-4.1.2.1-5.6.1)

"state":

REQUIRED if a `state` parameter was present in the client authorization request. The exact value received from the client.[¶](#section-4.1.2.1-5.8.1)

"iss":

OPTIONAL. The identifier of the authorization server. See [Section 4.1.2](#authorization-response) above for details.[¶](#section-4.1.2.1-5.10.1)

For example, the authorization server indicates the request was denied by redirecting the user agent with the following HTTP response:[¶](#section-4.1.2.1-6)

```
HTTP/1.1 302 Found
Location: https://client.example.com/cb?error=access_denied
          &state=xyz&iss=https%3A%2F%2Fauthorization-server.example.com
```

[¶](#section-4.1.2.1-7)

#### [4.1.3.](#section-4.1.3) [Token Endpoint Extension](#name-token-endpoint-extension)

The authorization grant type is identified at the token endpoint with the `grant_type` value of `authorization_code`.[¶](#section-4.1.3-1)

If this value is set, the following additional token request parameters beyond [Section 3.2.2](#token-request) are supported:[¶](#section-4.1.3-2)

"code":

REQUIRED. The authorization code received from the authorization server.[¶](#section-4.1.3-3.2.1)

"code\_verifier":

REQUIRED, if the `code_challenge` parameter was included in the authorization request. MUST NOT be used otherwise. The original code verifier string.[¶](#section-4.1.3-3.4.1)

"client\_id":

REQUIRED, if the client is not authenticating with the authorization server as described in [Section 3.2.1](#token-endpoint-client-authentication).[¶](#section-4.1.3-3.6.1)

The authorization server MUST return an access token only once for a given authorization code.[¶](#section-4.1.3-4)

If a second valid token request is made with the same authorization code as a previously successful token request, the authorization server MUST deny the request and SHOULD revoke (when possible) all access tokens and refresh tokens previously issued based on that authorization code. See [Section 7.5.2](#authorization-code-reuse) for further details.[¶](#section-4.1.3-5)

For example, the client makes the following HTTPS request (with extra line breaks for display purposes only):[¶](#section-4.1.3-6)

```
POST /token HTTP/1.1
Host: server.example.com
Authorization: Basic czZCaGRSa3F0MzpnWDFmQmF0M2JW
Content-Type: application/x-www-form-urlencoded

grant_type=authorization_code
&code=SplxlOBeZQQYbYS6WxSbIA
&code_verifier=3641a2d12d66101249cdf7a79c000c1f8c05d2aafcf14bf146497bed
```

[¶](#section-4.1.3-7)

In addition to the processing rules in [Section 3.2.2](#token-request), the authorization server MUST:[¶](#section-4.1.3-8)

- ensure that the authorization code was issued to the authenticated confidential client, or if the client is public, ensure that the code was issued to `client_id` in the request,[¶](#section-4.1.3-9.1.1)
- verify that the authorization code is valid,[¶](#section-4.1.3-9.2.1)
- verify that the `code_verifier` parameter is present if and only if a `code_challenge` parameter was present in the authorization request,[¶](#section-4.1.3-9.3.1)
- if a `code_verifier` is present, verify the `code_verifier` by calculating the code challenge from the received `code_verifier` and comparing it with the previously associated `code_challenge`, after first transforming it according to the `code_challenge_method` method specified by the client, and[¶](#section-4.1.3-9.4.1)
- If there was no `code_challenge` in the authorization request associated with the authorization code in the token request, the authorization server MUST reject the token request.[¶](#section-4.1.3-9.5.1)

See [Section 10.2](#redirect-uri-in-token-request) for details on backwards compatibility with OAuth 2.0 clients regarding the `redirect_uri` parameter in the token request.[¶](#section-4.1.3-10)

### [4.2.](#section-4.2) [Client Credentials Grant](#name-client-credentials-grant)

The client can request an access token using only its client credentials (or other supported means of authentication) when the client is requesting access to the protected resources under its control, or those of another resource owner that have been previously arranged with the authorization server (the method of which is beyond the scope of this specification).[¶](#section-4.2-1)

The client credentials grant type MUST only be used by confidential clients.[¶](#section-4.2-2)

```
     +---------+                                  +---------------+
     |         |                                  |               |
     |         |>--(1)- Client Authentication --->| Authorization |
     | Client  |                                  |     Server    |
     |         |<--(2)---- Access Token ---------<|               |
     |         |                                  |               |
     +---------+                                  +---------------+
```

[Figure 4](#figure-4): [Client Credentials Grant](#name-client-credentials-grant-2)

The use of the client credentials grant illustrated in [Figure 4](#fig-client-credentials-grant) includes the following steps:[¶](#section-4.2-4)

(1) The client authenticates with the authorization server and requests an access token from the token endpoint.[¶](#section-4.2-5)

(2) The authorization server authenticates the client, and if valid, issues an access token.[¶](#section-4.2-6)

#### [4.2.1.](#section-4.2.1) [Token Endpoint Extension](#name-token-endpoint-extension-2)

The client credentials grant type is identified at the token endpoint with the `grant_type` value of `client_credentials`.[¶](#section-4.2.1-1)

If this value is set, the following additional token request parameters beyond [Section 3.2.2](#token-request) are supported:[¶](#section-4.2.1-2)

"scope":

OPTIONAL. The scope of the access request as described by [Section 1.4.1](#access-token-scope).[¶](#section-4.2.1-3.2.1)

For example, the client makes the following HTTP request using transport-layer security (with extra line breaks for display purposes only):[¶](#section-4.2.1-4)

```
POST /token HTTP/1.1
Host: server.example.com
Authorization: Basic czZCaGRSa3F0MzpnWDFmQmF0M2JW
Content-Type: application/x-www-form-urlencoded

grant_type=client_credentials
```

[¶](#section-4.2.1-5)

The authorization server MUST authenticate the client.[¶](#section-4.2.1-6)

### [4.3.](#section-4.3) [Refresh Token Grant](#name-refresh-token-grant)

The refresh token is a credential issued by the authorization server to a client, which can be used to obtain new (fresh) access tokens based on an existing grant. The client uses this option either because the previous access token has expired or the client previously obtained an access token with a scope more narrow than approved by the respective grant and later requires an access token with a different scope under the same grant.[¶](#section-4.3-1)

Refresh tokens MUST be kept confidential in transit and storage, and shared only among the authorization server and the client to whom the refresh tokens were issued. The authorization server MUST maintain the binding between a refresh token and the client to whom it was issued.[¶](#section-4.3-2)

The authorization server MUST verify the binding between the refresh token and client identity whenever the client identity can be authenticated. When client authentication is not possible, the authorization server SHOULD issue sender-constrained refresh tokens or use refresh token rotation as described in [Section 4.3.1](#refresh-token-endpoint-extension).[¶](#section-4.3-3)

The authorization server MUST ensure that refresh tokens cannot be generated, modified, or guessed to produce valid refresh tokens by unauthorized parties.[¶](#section-4.3-4)

#### [4.3.1.](#section-4.3.1) [Token Endpoint Extension](#name-token-endpoint-extension-3)

The refresh token grant type is identified at the token endpoint with the `grant_type` value of `refresh_token`.[¶](#section-4.3.1-1)

If this value is set, the following additional parameters beyond [Section 3.2.2](#token-request) are supported:[¶](#section-4.3.1-2)

"refresh\_token":

REQUIRED. The refresh token issued to the client.[¶](#section-4.3.1-3.2.1)

"scope":

OPTIONAL. The scope of the access request as described by [Section 1.4.1](#access-token-scope). The requested scope MUST NOT include any scope not originally granted by the resource owner, and if omitted is treated as equal to the scope originally granted by the resource owner.[¶](#section-4.3.1-3.4.1)

Because refresh tokens are typically long-lasting credentials used to request additional access tokens, the refresh token is bound to the client to which it was issued. Confidential clients MUST authenticate with the authorization server as described in [Section 3.2.1](#token-endpoint-client-authentication).[¶](#section-4.3.1-4)

For example, the client makes the following HTTP request using transport-layer security (with extra line breaks for display purposes only):[¶](#section-4.3.1-5)

```
POST /token HTTP/1.1
Host: server.example.com
Authorization: Basic czZCaGRSa3F0MzpnWDFmQmF0M2JW
Content-Type: application/x-www-form-urlencoded

grant_type=refresh_token&refresh_token=tGzv3JOkF0XG5Qx2TlKWIA
```

[¶](#section-4.3.1-6)

In addition to the processing rules in [Section 3.2.2](#token-request), the authorization server MUST:[¶](#section-4.3.1-7)

- if client authentication is included in the request, ensure that the refresh token was issued to the authenticated client, OR if a client\_id is included in the request, ensure the refresh token was issued to the matching client[¶](#section-4.3.1-8.1.1)
- validate that the grant corresponding to this refresh token is still active[¶](#section-4.3.1-8.2.1)
- validate the refresh token[¶](#section-4.3.1-8.3.1)

Authorization servers MUST utilize one of these methods to detect refresh token replay by malicious actors for public clients:[¶](#section-4.3.1-9)

- *Sender-constrained refresh tokens:* the authorization server cryptographically binds the refresh token to a certain client instance, e.g., by utilizing DPoP \[[RFC9449](#RFC9449)] or mTLS \[[RFC8705](#RFC8705)].[¶](#section-4.3.1-10.1.1)
- *Refresh token rotation:* the authorization server issues a new refresh token with every access token refresh response. The previous refresh token is invalidated but information about the relationship is retained by the authorization server. If a refresh token is compromised and subsequently used by both the attacker and the legitimate client, one of them will present an invalidated refresh token, which will inform the authorization server of the breach. The authorization server cannot determine which party submitted the invalid refresh token, but it will revoke the active refresh token as well as the access authorization grant associated with it. This stops the attack at the cost of forcing the legitimate client to obtain a fresh authorization grant.[¶](#section-4.3.1-10.2.1)

Implementation note: the grant to which a refresh token belongs may be encoded into the refresh token itself. This can enable an authorization server to efficiently determine the grant to which a refresh token belongs, and by extension, all refresh tokens that need to be revoked. Authorization servers MUST ensure the integrity of the refresh token value in this case, for example, using signatures.[¶](#section-4.3.1-11)

#### [4.3.2.](#section-4.3.2) [Refresh Token Response](#name-refresh-token-response)

If valid and authorized, the authorization server issues an access token as described in [Section 3.2.3](#token-response).[¶](#section-4.3.2-1)

The authorization server MAY issue a new refresh token, in which case the client MUST discard the old refresh token and replace it with the new refresh token.[¶](#section-4.3.2-2)

#### [4.3.3.](#section-4.3.3) [Refresh Token Recommendations](#name-refresh-token-recommendatio)

The authorization server MAY revoke the old refresh token after issuing a new refresh token to the client. If a new refresh token is issued, the refresh token scope MUST be identical to that of the refresh token included by the client in the request.[¶](#section-4.3.3-1)

Authorization servers MAY revoke refresh tokens automatically in case of a security event, such as:[¶](#section-4.3.3-2)

- password change[¶](#section-4.3.3-3.1.1)
- logout at the authorization server[¶](#section-4.3.3-3.2.1)

Refresh tokens SHOULD expire if the client has been inactive for some time, i.e., the refresh token has not been used to obtain new access tokens for some time. The expiration time is at the discretion of the authorization server. It might be a global value or determined based on the client policy or the grant associated with the refresh token (and its sensitivity).[¶](#section-4.3.3-4)

### [4.4.](#section-4.4) [Extension Grants](#name-extension-grants)

The client uses an extension grant type by specifying the grant type using an absolute URI (defined by the authorization server) as the value of the `grant_type` parameter of the token endpoint, and by adding any additional parameters necessary.[¶](#section-4.4-1)

For example, to request an access token using the Device Authorization Grant as defined by \[[RFC8628](#RFC8628)] after the user has authorized the client on a separate device, the client makes the following HTTPS request (with extra line breaks for display purposes only):[¶](#section-4.4-2)

```
  POST /token HTTP/1.1
  Host: server.example.com
  Content-Type: application/x-www-form-urlencoded

  grant_type=urn%3Aietf%3Aparams%3Aoauth%3Agrant-type%3Adevice_code
  &device_code=GmRhmhcxhwEzkoEqiMEg_DnyEysNkuNhszIySk9eS
  &client_id=C409020731
```

[¶](#section-4.4-3)

If the access token request is valid and authorized, the authorization server issues an access token and optional refresh token as described in [Section 3.2.3](#token-response). If the request failed client authentication or is invalid, the authorization server returns an error response as described in [Section 3.2.4](#token-error-response).[¶](#section-4.4-4)

## [5.](#section-5) [Resource Requests](#name-resource-requests)

The client accesses protected resources by presenting an access token to the resource server. The resource server MUST validate the access token and ensure that it has not expired and that its scope covers the requested resource. The methods used by the resource server to validate the access token are beyond the scope of this specification, but generally involve an interaction or coordination between the resource server and the authorization server. For example, when the resource server and authorization server are colocated or are part of the same system, they may share a database or other storage; when the two components are operated independently, they may use Token Introspection \[[RFC7662](#RFC7662)] or a structured access token format such as a JWT \[[RFC9068](#RFC9068)].[¶](#section-5-1)

### [5.1.](#section-5.1) [Bearer Token Requests](#name-bearer-token-requests)

This section defines two methods of sending Bearer tokens in resource requests to resource servers. Clients MUST use one of the two methods defined below, and MUST NOT use more than one method to transmit the token in each request.[¶](#section-5.1-1)

In particular, clients MUST NOT send the access token in a URI query parameter, and resource servers MUST ignore access tokens in a URI query parameter.[¶](#section-5.1-2)

#### [5.1.2.](#section-5.1.2) [Form-Encoded Content Parameter](#name-form-encoded-content-parame)

When sending the access token in the HTTP request content, the client adds the access token to the request content using the `access_token` parameter. The client MUST NOT use this method unless all of the following conditions are met:[¶](#section-5.1.2-1)

- The HTTP request includes the `Content-Type` header field set to `application/x-www-form-urlencoded`.[¶](#section-5.1.2-2.1.1)
- The content follows the encoding requirements of the `application/x-www-form-urlencoded` content-type as defined by the URL Living Standard \[[WHATWG.URL](#WHATWG.URL)].[¶](#section-5.1.2-2.2.1)
- The HTTP request content is single-part.[¶](#section-5.1.2-2.3.1)
- The content to be encoded in the request MUST consist entirely of ASCII \[[USASCII](#USASCII)] characters.[¶](#section-5.1.2-2.4.1)
- The HTTP request method is one for which the content has defined semantics. In particular, this means that the `GET` method MUST NOT be used.[¶](#section-5.1.2-2.5.1)

The content MAY include other request-specific parameters, in which case the `access_token` parameter MUST be properly separated from the request-specific parameters using `&` character(s) (ASCII code 38).[¶](#section-5.1.2-3)

For example, the client makes the following HTTP request using transport-layer security:[¶](#section-5.1.2-4)

```
POST /resource HTTP/1.1
Host: server.example.com
Content-Type: application/x-www-form-urlencoded

access_token=mF_9.B5f-4.1JqM
```

[¶](#section-5.1.2-5)

The `application/x-www-form-urlencoded` method SHOULD NOT be used except in application contexts where participating clients do not have access to the `Authorization` request header field. Resource servers MAY support this method.[¶](#section-5.1.2-6)

### [5.2.](#section-5.2) [Access Token Validation](#name-access-token-validation)

After receiving the access token, the resource server MUST check that the access token is not yet expired, is authorized to access the requested resource, was issued with the appropriate scope, and meets other policy requirements of the resource server to access the protected resource.[¶](#section-5.2-1)

Access tokens generally fall into two categories: reference tokens or self-encoded tokens. Reference tokens can be validated by querying the authorization server or looking up the token in a token database, whereas self-encoded tokens contain the authorization information in an encrypted and/or signed string which can be extracted by the resource server.[¶](#section-5.2-2)

A standardized method to query the authorization server to check the validity of an access token is defined in Token Introspection \[[RFC7662](#RFC7662)].[¶](#section-5.2-3)

A standardized method of encoding information in a token string is defined in JWT Profile for Access Tokens \[[RFC9068](#RFC9068)].[¶](#section-5.2-4)

See [Section 7.1](#access-token-security-considerations) for additional considerations around creating and validating access tokens.[¶](#section-5.2-5)

### [5.3.](#section-5.3) [Error Response](#name-error-response)

If a resource access request fails, the resource server SHOULD inform the client of the error. The details of the error response is determined by the particular token type, such as the description of Bearer tokens in [Section 5.3.2](#bearer-token-error-codes).[¶](#section-5.3-1)

#### [5.3.2.](#section-5.3.2) [Error Codes](#name-error-codes)

When a request fails, the resource server responds using the appropriate HTTP status code (typically, 400, 401, 403, or 405) and includes one of the following error codes in the response:[¶](#section-5.3.2-1)

"invalid\_request":

The request is missing a required parameter, includes an unsupported parameter or parameter value, repeats the same parameter, uses more than one method for including an access token, or is otherwise malformed. The resource server SHOULD respond with the HTTP 400 (Bad Request) status code.[¶](#section-5.3.2-2.2.1)

"invalid\_token":

The access token provided is expired, revoked, malformed, or invalid for other reasons. The resource server SHOULD respond with the HTTP 401 (Unauthorized) status code. The client MAY request a new access token and retry the protected resource request.[¶](#section-5.3.2-2.4.1)

"insufficient\_scope":

The request requires higher privileges (scopes) than provided by the scopes granted to the client and represented by the access token. The resource server SHOULD respond with the HTTP 403 (Forbidden) status code and MAY include the `scope` attribute with the scope necessary to access the protected resource.[¶](#section-5.3.2-2.6.1)

Extensions may define additional error codes or specify additional circumstances in which the above error codes are returned.[¶](#section-5.3.2-3)

If the request lacks any authentication information (e.g., the client was unaware that authentication is necessary or attempted using an unsupported authentication method), the resource server SHOULD NOT include an error code or other error information.[¶](#section-5.3.2-4)

For example:[¶](#section-5.3.2-5)

```
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer realm="example"
```

[¶](#section-5.3.2-6)

And in response to a protected resource request with an authentication attempt using an expired access token:[¶](#section-5.3.2-7)

```
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer realm="example",
                  error="invalid_token",
                  error_description="The access token expired"
```

[¶](#section-5.3.2-8)

## [6.](#section-6) [Extensibility](#name-extensibility)

### [6.1.](#section-6.1) [Defining Access Token Types](#name-defining-access-token-types)

Access token types can be defined in one of two ways: registered in the Access Token Types registry (following the procedures in [Section 11.1](https://rfc-editor.org/rfc/rfc6749#section-11.1) of \[[RFC6749](#RFC6749)]), or by using a unique absolute URI as its name.[¶](#section-6.1-1)

#### [6.1.1.](#section-6.1.1) [Registered Access Token Types](#name-registered-access-token-typ)

\[[RFC6750](#RFC6750)] establishes a common registry in [Section 11.4](https://rfc-editor.org/rfc/rfc6749#section-11.4) of \[[RFC6749](#RFC6749)] for error values to be shared among OAuth token authentication schemes.[¶](#section-6.1.1-1)

New authentication schemes designed primarily for OAuth token authentication SHOULD define a mechanism for providing an error status code to the client, in which the error values allowed are registered in the error registry established by this specification.[¶](#section-6.1.1-2)

Such schemes MAY limit the set of valid error codes to a subset of the registered values. If the error code is returned using a named parameter, the parameter name SHOULD be `error`.[¶](#section-6.1.1-3)

Other schemes capable of being used for OAuth token authentication, but not primarily designed for that purpose, MAY bind their error values to the registry in the same manner.[¶](#section-6.1.1-4)

New authentication schemes MAY choose to also specify the use of the `error_description` and `error_uri` parameters to return error information in a manner parallel to their usage in this specification.[¶](#section-6.1.1-5)

Type names MUST conform to the type-name ABNF. If the type definition includes a new HTTP authentication scheme, the type name SHOULD be identical to the HTTP authentication scheme name (as defined by \[[RFC2617](#RFC2617)]). The token type `example` is reserved for use in examples.[¶](#section-6.1.1-6)

```
type-name  = 1*name-char
name-char  = "-" / "." / "_" / DIGIT / ALPHA
```

[¶](#section-6.1.1-7)

#### [6.1.2.](#section-6.1.2) [Vendor-Specific Access Token Types](#name-vendor-specific-access-toke)

Types utilizing a URI name SHOULD be limited to vendor-specific implementations that are not commonly applicable, and are specific to the implementation details of the resource server where they are used.[¶](#section-6.1.2-1)

All other types MUST be registered.[¶](#section-6.1.2-2)

### [6.2.](#section-6.2) [Defining New Endpoint Parameters](#name-defining-new-endpoint-param)

New request or response parameters for use with the authorization endpoint or the token endpoint are defined and registered in the OAuth Parameters registry following the procedure in [Section 11.2](https://rfc-editor.org/rfc/rfc6749#section-11.2) of \[[RFC6749](#RFC6749)].[¶](#section-6.2-1)

Parameter names MUST conform to the param-name ABNF, and parameter values syntax MUST be well-defined (e.g., using ABNF, or a reference to the syntax of an existing parameter).[¶](#section-6.2-2)

```
param-name  = 1*name-char
name-char   = "-" / "." / "_" / DIGIT / ALPHA
```

[¶](#section-6.2-3)

Unregistered vendor-specific parameter extensions that are not commonly applicable and that are specific to the implementation details of the authorization server where they are used SHOULD utilize a vendor-specific prefix that is not likely to conflict with other registered values (e.g., begin with 'companyname\_').[¶](#section-6.2-4)

### [6.3.](#section-6.3) [Defining New Authorization Grant Types](#name-defining-new-authorization-)

New authorization grant types can be defined by assigning them a unique absolute URI for use with the `grant_type` parameter. If the extension grant type requires additional token endpoint parameters, they MUST be registered in the OAuth Parameters registry as described by [Section 11.2](https://rfc-editor.org/rfc/rfc6749#section-11.2) of \[[RFC6749](#RFC6749)].[¶](#section-6.3-1)

### [6.4.](#section-6.4) [Defining New Authorization Endpoint Response Types](#name-defining-new-authorization-e)

New response types for use with the authorization endpoint are defined and registered in the Authorization Endpoint Response Types registry following the procedure in [Section 11.3](https://rfc-editor.org/rfc/rfc6749#section-11.3) of \[[RFC6749](#RFC6749)]. Response type names MUST conform to the response-type ABNF.[¶](#section-6.4-1)

```
response-type  = response-name *( SP response-name )
response-name  = 1*response-char
response-char  = "_" / DIGIT / ALPHA
```

[¶](#section-6.4-2)

If a response type contains one or more space characters (%x20), it is compared as a space-delimited list of values in which the order of values does not matter. Only one order of values can be registered, which covers all other arrangements of the same set of values.[¶](#section-6.4-3)

For example, an extension can define and register the `code other_token` response type. Once registered, the same combination cannot be registered as `other_token code`, but both values can be used to denote the same response type.[¶](#section-6.4-4)

### [6.5.](#section-6.5) [Defining Additional Error Codes](#name-defining-additional-error-c)

In cases where protocol extensions (i.e., access token types, extension parameters, or extension grant types) require additional error codes to be used with the authorization code grant error response ([Section 4.1.2.1](#authorization-code-error-response)), the token error response ([Section 3.2.4](#token-error-response)), or the resource access error response ([Section 5.3](#error-response)), such error codes MAY be defined.[¶](#section-6.5-1)

Extension error codes MUST be registered (following the procedures in [Section 11.4](https://rfc-editor.org/rfc/rfc6749#section-11.4) of \[[RFC6749](#RFC6749)]) if the extension they are used in conjunction with is a registered access token type, a registered endpoint parameter, or an extension grant type. Error codes used with unregistered extensions MAY be registered.[¶](#section-6.5-2)

Error codes MUST conform to the error ABNF and SHOULD be prefixed by an identifying name when possible. For example, an error identifying an invalid value set to the extension parameter `example` SHOULD be named `example_invalid`.[¶](#section-6.5-3)

```
error      = 1*error-char
error-char = %x20-21 / %x23-5B / %x5D-7E
```

[¶](#section-6.5-4)

## [7.](#section-7) [Security Considerations](#name-security-considerations)

As a flexible and extensible framework, OAuth's security considerations depend on many factors. The following sections provide implementers with security guidelines focused on the three client profiles described in [Section 2.1](#client-types): web application, browser-based application, and native application.[¶](#section-7-1)

A comprehensive OAuth security model and analysis, as well as background for the protocol design, is provided by \[[RFC6819](#RFC6819)] and \[[RFC9700](#RFC9700)].[¶](#section-7-2)

### [7.1.](#section-7.1) [Access Token Security Considerations](#name-access-token-security-consi)

#### [7.1.1.](#section-7.1.1) [Security Threats](#name-security-threats)

The following list presents several common threats against protocols utilizing some form of tokens. This list of threats is based on NIST Special Publication 800-63 \[[NIST800-63](#NIST800-63)].[¶](#section-7.1.1-1)

##### [7.1.1.1.](#section-7.1.1.1) [Access token manufacture/modification](#name-access-token-manufacture-mo)

An attacker may generate a bogus access token or modify the token contents (such as the authentication or attribute statements) of an existing token, causing the resource server to grant inappropriate access to the client. For example, an attacker may modify the token to extend the validity period; a malicious client may modify the assertion to gain access to information that they should not be able to view.[¶](#section-7.1.1.1-1)

##### [7.1.1.2.](#section-7.1.1.2) [Access token information disclosure](#name-access-token-information-di)

Access tokens may contain authentication and attribute statements that include sensitive information.[¶](#section-7.1.1.2-1)

If the client should be prevented from observing the contents of the access token, content encryption MUST be applied.[¶](#section-7.1.1.2-2)

Since cookies are by default transmitted in cleartext, any information contained in them is at risk of disclosure: Bearer tokens MUST NOT be stored in cookies that can be sent in the clear. See Section 7 and 8 of \[[RFC6265](#RFC6265)] for security considerations about cookies.[¶](#section-7.1.1.2-3)

##### [7.1.1.3.](#section-7.1.1.3) [Access token redirect](#name-access-token-redirect)

An attacker uses an access token generated for consumption by one resource server to gain access to a different resource server that mistakenly believes the token to be for it.[¶](#section-7.1.1.3-1)

##### [7.1.1.4.](#section-7.1.1.4) [Access token replay](#name-access-token-replay)

An attacker attempts to use an access token that has already been used with that resource server in the past.[¶](#section-7.1.1.4-1)

#### [7.1.2.](#section-7.1.2) [Threat Mitigation](#name-threat-mitigation)

A large range of threats can be mitigated by protecting the contents of the access token by using a digital signature, and by following best practices for signing key management such as periodic key rotation.[¶](#section-7.1.2-1)

Alternatively, a bearer token can contain a reference to authorization information, rather than encoding the information directly. Using a reference may require an extra interaction between a resource server and authorization server to resolve the reference to the authorization information. The mechanics of such an interaction are not defined by this specification, but one such mechanism is defined in Token Introspection \[[RFC7662](#RFC7662)].[¶](#section-7.1.2-2)

This document does not specify the encoding or the contents of the access token; hence, detailed recommendations about the means of guaranteeing access token integrity protection are outside the scope of this specification. One example of an encoding and signing mechanism for access tokens is described in JSON Web Token Profile for Access Tokens \[[RFC9068](#RFC9068)].[¶](#section-7.1.2-3)

To deal with access token redirects, it is important for the authorization server to include the identity of the intended recipients (the audience), typically a single resource server (or a list of resource servers), in the token. Restricting the use of the token to a specific scope is also RECOMMENDED.[¶](#section-7.1.2-4)

[Section 1.5](#communication-security) provides information to protect against access token disclosure and providing confidentiality and integrity for the communications between client, resource server and authorization server.[¶](#section-7.1.2-5)

#### [7.1.3.](#section-7.1.3) [Summary of Recommendations](#name-summary-of-recommendations)

##### [7.1.3.1.](#section-7.1.3.1) [Safeguard bearer tokens](#name-safeguard-bearer-tokens)

Client implementations MUST ensure that bearer tokens are not leaked to unintended parties, as they will be able to use them to gain access to protected resources. This is the primary security consideration when using bearer tokens and underlies all the more specific recommendations that follow.[¶](#section-7.1.3.1-1)

##### [7.1.3.2.](#section-7.1.3.2) [Validate TLS certificate chains](#name-validate-tls-certificate-ch)

The client MUST validate the TLS certificate chain when making requests to protected resources. Failing to do so may enable DNS hijacking attacks to steal the token and gain unintended access.[¶](#section-7.1.3.2-1)

##### [7.1.3.3.](#section-7.1.3.3) [Always use TLS (https)](#name-always-use-tls-https)

Clients MUST always use TLS (https) or equivalent transport security when making requests with bearer tokens. Failing to do so exposes the token to numerous attacks that could give attackers unintended access.[¶](#section-7.1.3.3-1)

##### [7.1.3.4.](#section-7.1.3.4) [Don't store bearer tokens in HTTP cookies](#name-dont-store-bearer-tokens-in)

Implementations MUST NOT store bearer tokens within cookies that can be sent in the clear (which is the default transmission mode for cookies). Implementations that do store bearer tokens in cookies MUST take precautions against cross-site request forgery.[¶](#section-7.1.3.4-1)

##### [7.1.3.5.](#section-7.1.3.5) [Issue short-lived bearer tokens](#name-issue-short-lived-bearer-to)

Authorization servers SHOULD issue short-lived bearer tokens, particularly when issuing tokens to clients that run within a web browser or other environments where information leakage may occur. Using short-lived bearer tokens can reduce the impact of them being leaked.[¶](#section-7.1.3.5-1)

##### [7.1.3.6.](#section-7.1.3.6) [Issue scoped bearer tokens](#name-issue-scoped-bearer-tokens)

Authorization servers SHOULD issue bearer tokens that contain an audience restriction, scoping their use to the intended resource server or set of resource servers.[¶](#section-7.1.3.6-1)

##### [7.1.3.7.](#section-7.1.3.7) [Don't pass bearer tokens in page URLs](#name-dont-pass-bearer-tokens-in-)

Bearer tokens MUST NOT be passed in page URLs (for example, as query string parameters). Instead, bearer tokens SHOULD be passed in HTTP message headers or message bodies for which confidentiality measures are taken. Browsers, web servers, and other software may not adequately secure URLs in the browser history, web server logs, and other data structures. If bearer tokens are passed in page URLs, attackers might be able to steal them from the history data, logs, or other unsecured locations.[¶](#section-7.1.3.7-1)

#### [7.1.4.](#section-7.1.4) [Access Token Privilege Restriction](#name-access-token-privilege-rest)

The privileges associated with an access token SHOULD be restricted to the minimum required for the particular application or use case. This prevents clients from exceeding the privileges authorized by the resource owner. It also prevents users from exceeding their privileges authorized by the respective security policy. Privilege restrictions also help to reduce the impact of access token leakage.[¶](#section-7.1.4-1)

In particular, access tokens SHOULD be restricted to certain resource servers (audience restriction), preferably to a single resource server. To put this into effect, the authorization server associates the access token with certain resource servers and every resource server is obliged to verify, for every request, whether the access token sent with that request was meant to be used for that particular resource server. If not, the resource server MUST refuse to serve the respective request. Clients and authorization servers MAY utilize the parameters `scope` or `resource` as specified in this document and \[[RFC8707](#RFC8707)], respectively, to determine the resource server they want to access.[¶](#section-7.1.4-2)

Additionally, access tokens SHOULD be restricted to certain resources and actions on resource servers or resources. To put this into effect, the authorization server associates the access token with the respective resource and actions and every resource server is obliged to verify, for every request, whether the access token sent with that request was meant to be used for that particular action on the particular resource. If not, the resource server must refuse to serve the respective request. Clients and authorization servers MAY utilize the parameter `scope` and `authorization_details` as specified in \[[RFC9396](#RFC9396)] to determine those resources and/or actions.[¶](#section-7.1.4-3)

### [7.2.](#section-7.2) [Client Authentication](#name-client-authentication-3)

Depending on the overall process of client registration and credential lifecycle management, this may affect the confidence an authorization server has in a particular client.[¶](#section-7.2-1)

For example, authentication of a dynamically registered client does not prove the identity of the client, it only ensures that repeated requests to the authorization server were made from the same client instance. Such clients may be limited in terms of which scopes they are allowed to request, or may have other limitations such as shorter token lifetimes.[¶](#section-7.2-2)

In contrast, if there is a registered application whose developer's identity was verified, who signed a contract and is issued a client secret that is only used in a secure backend service, the authorization server might allow this client to request more sensitive scopes or to be issued longer-lasting tokens.[¶](#section-7.2-3)

### [7.3.](#section-7.3) [Client Impersonation](#name-client-impersonation)

If a confidential client has its credentials stolen, a malicious client can impersonate the client and obtain access to protected resources.[¶](#section-7.3-1)

The authorization server SHOULD enforce explicit resource owner authentication and provide the resource owner with information about the client and the requested authorization scope and lifetime. It is up to the resource owner to review the information in the context of the current client and to authorize or deny the request.[¶](#section-7.3-2)

The authorization server SHOULD NOT process repeated authorization requests automatically (without active resource owner interaction) without authenticating the client or relying on other measures to ensure that the repeated request comes from the original client and not an impersonator.[¶](#section-7.3-3)

#### [7.3.1.](#section-7.3.1) [Impersonation of Native Apps](#name-impersonation-of-native-app)

As stated above, the authorization server SHOULD NOT process authorization requests automatically without user consent or interaction, except when the identity of the client can be assured. This includes the case where the user has previously approved an authorization request for a given client ID -- unless the identity of the client can be proven, the request SHOULD be processed as if no previous request had been approved.[¶](#section-7.3.1-1)

Measures such as claimed `https` scheme redirects MAY be accepted by authorization servers as identity proof. Some operating systems may offer alternative platform-specific identity features that MAY be accepted, as appropriate.[¶](#section-7.3.1-2)

#### [7.3.2.](#section-7.3.2) [Access Token Privilege Restriction](#name-access-token-privilege-restr)

The client SHOULD request access tokens with the minimal scope necessary. The authorization server SHOULD take the client identity into account when choosing how to honor the requested scope and MAY issue an access token with fewer scopes than requested.[¶](#section-7.3.2-1)

The privileges associated with an access token SHOULD be restricted to the minimum required for the particular application or use case. This prevents clients from exceeding the privileges authorized by the resource owner. It also prevents users from exceeding their privileges authorized by the respective security policy. Privilege restrictions also help to reduce the impact of access token leakage.[¶](#section-7.3.2-2)

In particular, access tokens SHOULD be restricted to certain resource servers (audience restriction), preferably to a single resource server. To put this into effect, the authorization server associates the access token with certain resource servers and every resource server is obliged to verify, for every request, whether the access token sent with that request was meant to be used for that particular resource server. If not, the resource server MUST refuse to serve the respective request. Clients and authorization servers MAY utilize the parameters `scope` or `resource` as specified in \[[RFC8707](#RFC8707)], respectively, to determine the resource server they want to access.[¶](#section-7.3.2-3)

### [7.4.](#section-7.4) [Client Impersonating Resource Owner](#name-client-impersonating-resour)

Resource servers may make access control decisions based on the identity of a resource owner for which an access token was issued, or based on the identity of a client in the client credentials grant. If both options are possible, depending on the details of the implementation, a client's identity may be mistaken for the identity of a resource owner. For example, if a client is able to choose its own `client_id` during registration with the authorization server, a malicious client may set it to a value identifying an end user (e.g., a `sub` value if OpenID Connect is used). If the resource server cannot properly distinguish between access tokens issued to clients and access tokens issued to end users, the client may then be able to access resource of the end user.[¶](#section-7.4-1)

If the authorization server has a common namespace for client IDs and user identifiers, causing the resource server to be unable to distinguish an access token authorized by a resource owner from an access token authorized by a client itself, authorization servers SHOULD NOT allow clients to influence their `client_id` or any other Claim if that can cause confusion with a genuine resource owner. Where this cannot be avoided, authorization servers MUST provide other means for the resource server to distinguish between the two types of access tokens.[¶](#section-7.4-2)

### [7.5.](#section-7.5) [Authorization Code Security Considerations](#name-authorization-code-security)

#### [7.5.1.](#section-7.5.1) [Authorization Code Injection](#name-authorization-code-injectio)

Authorization code injection is an attack where the client receives an authorization code from the attacker in its redirect URI instead of the authorization code from the legitimate authorization server. Without protections in place, there is no mechanism by which the client can know that the attack has taken place. Authorization code injection can lead to both the attacker obtaining access to a victim's account, as well as a victim accidentally gaining access to the attacker's account.[¶](#section-7.5.1-1)

##### [7.5.1.1.](#section-7.5.1.1) [Countermeasures](#name-countermeasures)

To prevent injection of authorization codes into the client, using `code_challenge` and `code_verifier` is REQUIRED for clients, and authorization servers MUST enforce their use, unless both of the following criteria are met:[¶](#section-7.5.1.1-1)

- The client is a confidential client.[¶](#section-7.5.1.1-2.1.1)
- In the specific deployment and the specific request, there is reasonable assurance by the authorization server that the client implements the OpenID Connect `nonce` mechanism properly.[¶](#section-7.5.1.1-2.2.1)

In this case, using and enforcing `code_challenge` and `code_verifier` is still RECOMMENDED.[¶](#section-7.5.1.1-3)

The `code_challenge` or OpenID Connect `nonce` value MUST be transaction-specific and securely bound to the client and the user agent in which the transaction was started. If a transaction leads to an error, fresh values for `code_challenge` or `nonce` MUST be chosen.[¶](#section-7.5.1.1-4)

Relying on the client to validate the OpenID Connect `nonce` parameter means the authorization server has no way to confirm that the client has actually protected itself against authorization code injection attacks. If an attacker is able to inject an authorization code into a client, the client would still exchange the injected authorization code and obtain tokens, and would only later reject the ID token after validating the `nonce` and seeing that it doesn't match. In contrast, the authorization server enforcing the `code_challenge` and `code_verifier` parameters provides a higher security outcome, since the authorization server is able to recognize the authorization code injection attack pre-emptively and avoid issuing any tokens in the first place.[¶](#section-7.5.1.1-5)

Historic note: Although PKCE \[[RFC7636](#RFC7636)] (where the `code_challenge` and `code_verifier` parameters were created) was originally designed as a mechanism to protect native apps from authorization code exfiltration attacks, all kinds of OAuth clients, including web applications and other confidential clients, are susceptible to authorization code injection attacks, which are solved by the `code_challenge` and `code_verifier` mechanism.[¶](#section-7.5.1.1-6)

#### [7.5.2.](#section-7.5.2) [Reuse of Authorization Codes](#name-reuse-of-authorization-code)

Several types of attacks are possible if authorization codes are able to be used more than once.[¶](#section-7.5.2-1)

As described in [Section 4.1.3](#code-token-extension), the authorization server must reject a token request and revoke any issued tokens when receiving a second valid request with an authorization code that has already been used to issue an access token. If an attacker is able to exfiltrate an authorization code and use it before the legitimate client, the attacker will obtain the access token and the legitimate client will not. Revoking any issued tokens means the attacker's tokens will then be revoked, stopping the attack from proceeding any further.[¶](#section-7.5.2-2)

However, the authorization server should only revoke issued tokens if the request containing the authorization code is also valid, including any other parameters such as the `code_verifier` and client authentication. The authorization server SHOULD NOT revoke any issued tokens when receiving a replayed authorization code that contains invalid parameters. If it were to do so, this would create a denial of service opportunity for an attacker who is able to obtain an authorization code but unable to obtain the client authentication or `code_verifier` by sending an invalid authorization code request before the legitimate client and thereby revoking the legitimate client's tokens once it makes the valid request.[¶](#section-7.5.2-3)

#### [7.5.3.](#section-7.5.3) [HTTP 307 Redirect](#name-http-307-redirect)

An authorization server which redirects a request that potentially contains user credentials MUST NOT use the 307 status code ([Section 15.4.8](https://rfc-editor.org/rfc/rfc9110#section-15.4.8) of \[[RFC9110](#RFC9110)]) for redirection. If an HTTP redirection (and not, for example, JavaScript) is used for such a request, AS SHOULD use the status code 303 ("See Other").[¶](#section-7.5.3-1)

At the authorization endpoint, a typical protocol flow is that the AS prompts the user to enter their credentials in a form that is then submitted (using the POST method) back to the authorization server. The AS checks the credentials and, if successful, redirects the user agent to the client's redirect URI.[¶](#section-7.5.3-2)

If the status code 307 were used for redirection, the user agent would send the user credentials via a POST request to the client.[¶](#section-7.5.3-3)

This discloses the sensitive credentials to the client. If the client is malicious, it can use the credentials to impersonate the user at the AS.[¶](#section-7.5.3-4)

The behavior might be unexpected for developers, but is defined in [Section 15.4.8](https://rfc-editor.org/rfc/rfc9110#section-15.4.8) of \[[RFC9110](#RFC9110)]. This status code does not require the user agent to rewrite the POST request to a GET request and thereby drop the form data in the POST request content.[¶](#section-7.5.3-5)

In HTTP \[[RFC9110](#RFC9110)], only the status code 303 unambiguously enforces rewriting the HTTP POST request to an HTTP GET request. For all other status codes, including the popular 302, user agents can opt not to rewrite POST to GET requests and therefore reveal the user credentials to the client. (In practice, however, most user agents will only show this behaviour for 307 redirects.)[¶](#section-7.5.3-6)

### [7.6.](#section-7.6) [Ensuring Endpoint Authenticity](#name-ensuring-endpoint-authentic)

The risk related to man-in-the-middle attacks is mitigated by the mandatory use of channel security mechanisms such as \[[RFC8446](#RFC8446)] for communicating with the Authorization and Token Endpoints. See [Section 1.5](#communication-security) for further details.[¶](#section-7.6-1)

### [7.7.](#section-7.7) [Credentials-Guessing Attacks](#name-credentials-guessing-attack)

The authorization server MUST prevent attackers from guessing access tokens, authorization codes, refresh tokens, resource owner passwords, and client credentials.[¶](#section-7.7-1)

The probability of an attacker guessing generated tokens (and other credentials not intended for handling by end users) MUST be less than or equal to 2^(-128) and SHOULD be less than or equal to 2^(-160).[¶](#section-7.7-2)

The authorization server MUST utilize other means to protect credentials intended for end-user usage.[¶](#section-7.7-3)

### [7.8.](#section-7.8) [Phishing Attacks](#name-phishing-attacks)

Wide deployment of this and similar protocols may cause end users to become inured to the practice of being redirected to websites where they are asked to enter their passwords. If end users are not careful to verify the authenticity of these websites before entering their credentials, it will be possible for attackers to exploit this practice to steal resource owners' passwords, and other phishable credentials such as OTPs.[¶](#section-7.8-1)

Service providers should attempt to educate end users about the risks phishing attacks pose and should provide mechanisms that make it easy for end users to confirm the authenticity of their sites, such as using phishing-resistant authenticators, as phishing resistant authenticators will offer a credential to log in to a certain site to the user only if the platform has successfully verified the site's origin. Client developers should consider the security implications of how they interact with the user agent (e.g., external, embedded), and the ability of the end user to verify the authenticity of the authorization server.[¶](#section-7.8-2)

See [Section 1.5](#communication-security) for further details on mitigating the risk of phishing attacks.[¶](#section-7.8-3)

### [7.9.](#section-7.9) [Cross-Site Request Forgery](#name-cross-site-request-forgery)

An attacker might attempt to inject a request to the redirect URI of the legitimate client on the victim's device, e.g., to cause the client to access resources under the attacker's control. This is a variant of an attack known as Cross-Site Request Forgery (CSRF).[¶](#section-7.9-1)

The traditional countermeasure is that clients pass a random value, also known as a CSRF Token, in the `state` parameter that links the request to the redirect URI to the user agent session as described. This countermeasure is described in detail in [Section 5.3.5](https://rfc-editor.org/rfc/rfc6819#section-5.3.5) of \[[RFC6819](#RFC6819)]. The same protection is provided by the `code_verifier` parameter or the OpenID Connect `nonce` value.[¶](#section-7.9-2)

When using `code_verifier` instead of `state` or `nonce` for CSRF protection, it is important to note that:[¶](#section-7.9-3)

- Clients MUST ensure that the AS supports the `code_challenge_method` intended to be used by the client. If an authorization server does not support the requested method, `state` or `nonce` MUST be used for CSRF protection instead.[¶](#section-7.9-4.1.1)
- If `state` is used for carrying application state, and integrity of its contents is a concern, clients MUST protect `state` against tampering and swapping. This can be achieved by binding the contents of state to the browser session and/or signed/encrypted state values \[[I-D.bradley-oauth-jwt-encoded-state](#I-D.bradley-oauth-jwt-encoded-state)].[¶](#section-7.9-4.2.1)

AS therefore MUST provide a way to detect their supported code challenge methods either via AS metadata according to \[[RFC8414](#RFC8414)] or provide a deployment-specific way to ensure or determine support.[¶](#section-7.9-5)

### [7.10.](#section-7.10) [Clickjacking](#name-clickjacking)

As described in [Section 4.4.1.9](https://rfc-editor.org/rfc/rfc6819#section-4.4.1.9) of \[[RFC6819](#RFC6819)], the authorization request is susceptible to clickjacking attacks, also called user interface redressing. In such an attack, an attacker embeds the authorization endpoint user interface in an innocuous context. A user believing to interact with that context, for example, clicking on buttons, inadvertently interacts with the authorization endpoint user interface instead. The opposite can be achieved as well: A user believing to interact with the authorization endpoint might inadvertently type a password into an attacker-provided input field overlaid over the original user interface. Clickjacking attacks can be designed such that users can hardly notice the attack, for example using almost invisible iframes overlaid on top of other elements.[¶](#section-7.10-1)

An attacker can use this vector to obtain the user's authentication credentials, change the scope of access granted to the client, and potentially access the user's resources.[¶](#section-7.10-2)

Authorization servers MUST prevent clickjacking attacks. Multiple countermeasures are described in \[[RFC6819](#RFC6819)], including the use of the `X-Frame-Options` HTTP response header field and frame-busting JavaScript. In addition to those, authorization servers SHOULD also use Content Security Policy (CSP) level 2 \[[CSP-2](#CSP-2)] or greater.[¶](#section-7.10-3)

To be effective, CSP must be used on the authorization endpoint and, if applicable, other endpoints used to authenticate the user and authorize the client (e.g., the device authorization endpoint, login pages, error pages, etc.). This prevents framing by unauthorized origins in user agents that support CSP. The client MAY permit being framed by some other origin than the one used in its redirection endpoint. For this reason, authorization servers SHOULD allow administrators to configure allowed origins for particular clients and/or for clients to register these dynamically.[¶](#section-7.10-4)

Using CSP allows authorization servers to specify multiple origins in a single response header field and to constrain these using flexible patterns (see \[[CSP-2](#CSP-2)] for details). Level 2 of this standard provides a robust mechanism for protecting against clickjacking by using policies that restrict the origin of frames (using `frame-ancestors`) together with those that restrict the sources of scripts allowed to execute on an HTML page (by using `script-src`). A non-normative example of such a policy is shown in the following listing:[¶](#section-7.10-5)

```
HTTP/1.1 200 OK
Content-Security-Policy: frame-ancestors https://ext.example.org:8000
Content-Security-Policy: script-src 'self'
X-Frame-Options: ALLOW-FROM https://ext.example.org:8000
...
```

[¶](#section-7.10-6)

Because some user agents do not support \[[CSP-2](#CSP-2)], this technique SHOULD be combined with others, including those described in \[[RFC6819](#RFC6819)], unless such legacy user agents are explicitly unsupported by the authorization server. Even in such cases, additional countermeasures SHOULD still be employed.[¶](#section-7.10-7)

### [7.11.](#section-7.11) [Injection and Input Validation](#name-injection-and-input-validat)

An injection attack occurs when an input or otherwise external variable is used by an application unsanitized and causes modification to the application logic. This may allow an attacker to gain access to the application device or its data, cause denial of service, or introduce a wide range of malicious side-effects.[¶](#section-7.11-1)

The authorization server and client MUST treat parameters received as potentially malicious external input and apply appropriate protections, in particular, the values of the `state` and `redirect_uri` parameters.[¶](#section-7.11-2)

### [7.12.](#section-7.12) [Open Redirection](#name-open-redirection)

An open redirector is an endpoint that forwards a user's browser to an arbitrary URI obtained from a query parameter. Such endpoints are sometimes implemented, for example, to show a message before a user is then redirected to an external website, or to redirect users back to a URL they were intending to visit before being interrupted, e.g., by a login prompt.[¶](#section-7.12-1)

The following attacks can occur when an AS or client has an open redirector.[¶](#section-7.12-2)

#### [7.12.1.](#section-7.12.1) [Client as Open Redirector](#name-client-as-open-redirector)

Clients MUST NOT expose open redirectors. Attackers may use open redirectors to produce URLs pointing to the client and utilize them to exfiltrate authorization codes, as described in [Section 4.1.1](https://rfc-editor.org/rfc/rfc9700#section-4.1.1) of \[[RFC9700](#RFC9700)]. Another abuse case is to produce URLs that appear to point to the client. This might trick users into trusting the URL and follow it in their browser. This can be abused for phishing.[¶](#section-7.12.1-1)

In order to prevent open redirection, clients should only redirect if the target URLs are whitelisted or if the origin and integrity of a request can be authenticated. Countermeasures against open redirection are described by OWASP \[[owasp\_redir](#owasp_redir)].[¶](#section-7.12.1-2)

#### [7.12.2.](#section-7.12.2) [Authorization Server as Open Redirector](#name-authorization-server-as-ope)

Just as with clients, attackers could try to utilize a user's trust in the authorization server (and its URL in particular) for performing phishing attacks. OAuth authorization servers regularly redirect users to other web sites (the clients), but must do so safely.[¶](#section-7.12.2-1)

[Section 4.1.2.1](#authorization-code-error-response) already prevents open redirects by stating that the authorization server MUST NOT automatically redirect the user agent in case of an invalid combination of `client_id` and `redirect_uri`.[¶](#section-7.12.2-2)

However, an attacker could also utilize a correctly registered redirect URI to perform phishing attacks. The attacker could, for example, register a client via dynamic client registration \[[RFC7591](#RFC7591)] and execute one of the following attacks:[¶](#section-7.12.2-3)

1. Intentionally send an erroneous authorization request, e.g., by using an invalid scope value, thus instructing the authorization server to redirect the user agent to its phishing site.[¶](#section-7.12.2-4.1.1)
2. Intentionally send a valid authorization request with `client_id` and `redirect_uri` controlled by the attacker. After the user authenticates, the authorization server prompts the user to provide consent to the request. If the user notices an issue with the request and declines the request, the authorization server still redirects the user agent to the phishing site. In this case, the user agent will be redirected to the phishing site regardless of the action taken by the user.[¶](#section-7.12.2-4.2.1)
3. Intentionally send a valid silent authentication request (`prompt=none`) with `client_id` and `redirect_uri` controlled by the attacker. In this case, the authorization server will automatically redirect the user agent to the phishing site.[¶](#section-7.12.2-4.3.1)

The authorization server MUST take precautions to prevent these threats. The authorization server MUST always authenticate the user first and, with the exception of the silent authentication use case, prompt the user for credentials when needed, before redirecting the user. Based on its risk assessment, the authorization server needs to decide whether it can trust the redirect URI or not. It could take into account URI analytics done internally or through some external service to evaluate the credibility and trustworthiness content behind the URI, and the source of the redirect URI and other client data.[¶](#section-7.12.2-5)

The authorization server SHOULD only automatically redirect the user agent if it trusts the redirect URI. If the URI is not trusted, the authorization server MAY inform the user and rely on the user to make the correct decision.[¶](#section-7.12.2-6)

### [7.13.](#section-7.13) [Transport Security](#name-transport-security)

In some deployments, including those utilizing load balancers, the TLS connection to the resource server terminates prior to the actual server that provides the resource. This could leave the token unprotected between the front-end server where the TLS connection terminates and the back-end server that provides the resource. In such deployments, sufficient measures MUST be employed to ensure confidentiality of the access token between the front-end and back- end servers; encryption of the token is one such possible measure.[¶](#section-7.13-1)

See [Section 17.2](https://rfc-editor.org/rfc/rfc9110#section-17.2) of \[[RFC9110](#RFC9110)] for further information.[¶](#section-7.13-2)

### [7.14.](#section-7.14) [Authorization Server Mix-Up Mitigation](#name-authorization-server-mix-up)

Mix-up is an attack on scenarios where an OAuth client interacts with two or more authorization servers and at least one authorization server is under the control of the attacker. This can be the case, for example, if the attacker uses dynamic registration to register the client at his own authorization server or if an authorization server becomes compromised.[¶](#section-7.14-1)

When an OAuth client can only interact with one authorization server, a mix-up defense is not required. In scenarios where an OAuth client interacts with two or more authorization servers, however, clients MUST prevent mix-up attacks. Two different methods are discussed in the following.[¶](#section-7.14-2)

For both defenses, clients MUST store, for each authorization request, the issuer they sent the authorization request to, bind this information to the user agent, and check that the authorization response was received from the correct issuer. Clients MUST ensure that the subsequent access token request, if applicable, is sent to the same issuer. The issuer serves, via the associated metadata, as an abstract identifier for the combination of the authorization endpoint and token endpoint that are to be used in the flow. If an issuer identifier is not available, for example, if neither OAuth 2.0 Authorization Server Metadata \[[RFC8414](#RFC8414)] nor OpenID Connect Discovery \[[OpenID.Discovery](#OpenID.Discovery)] are used, a different unique identifier for this tuple or the tuple itself can be used instead. For brevity of presentation, such a deployment-specific identifier will be subsumed under the issuer (or issuer identifier) in the following.[¶](#section-7.14-3)

Note: Just storing the authorization server URL is not sufficient to identify mix-up attacks. An attacker might declare an uncompromised AS's authorization endpoint URL as "their" AS URL, but declare a token endpoint under their own control.[¶](#section-7.14-4)

See [Section 4.4](https://rfc-editor.org/rfc/rfc9700#section-4.4) of \[[RFC9700](#RFC9700)] for a detailed description of several types of mix-up attacks.[¶](#section-7.14-5)

#### [7.14.1.](#section-7.14.1) [Mix-Up Defense via Issuer Identification](#name-mix-up-defense-via-issuer-i)

This defense requires that the authorization server sends his issuer identifier in the authorization response to the client. When receiving the authorization response, the client MUST compare the received issuer identifier to the stored issuer identifier. If there is a mismatch, the client MUST abort the interaction.[¶](#section-7.14.1-1)

There are different ways this issuer identifier can be transported to the client:[¶](#section-7.14.1-2)

- The issuer information can be transported, for example, via an optional response parameter `iss` (see [Section 4.1.2](#authorization-response)).[¶](#section-7.14.1-3.1.1)
- When OpenID Connect is used and an ID Token is returned in the authorization response, the client can evaluate the `iss` claim in the ID Token.[¶](#section-7.14.1-3.2.1)

In both cases, the `iss` value MUST be evaluated according to \[[RFC9207](#RFC9207)].[¶](#section-7.14.1-4)

While this defense may require using an additional parameter to transport the issuer information, it is a robust and relatively simple defense against mix-up.[¶](#section-7.14.1-5)

#### [7.14.2.](#section-7.14.2) [Mix-Up Defense via Distinct Redirect URIs](#name-mix-up-defense-via-distinct)

For this defense, clients MUST use a distinct redirect URI for each issuer they interact with.[¶](#section-7.14.2-1)

Clients MUST check that the authorization response was received from the correct issuer by comparing the distinct redirect URI for the issuer to the URI where the authorization response was received on. If there is a mismatch, the client MUST abort the flow.[¶](#section-7.14.2-2)

While this defense builds upon existing OAuth functionality, it cannot be used in scenarios where clients only register once for the use of many different issuers (as in some open banking schemes) and due to the tight integration with the client registration, it is harder to deploy automatically.[¶](#section-7.14.2-3)

Furthermore, an attacker might be able to circumvent the protection offered by this defense by registering a new client with the "honest" AS using the redirect URI that the client assigned to the attacker's AS. The attacker could then run the attack as described above, replacing the client ID with the client ID of his newly created client.[¶](#section-7.14.2-4)

This defense SHOULD therefore only be used if other options are not available.[¶](#section-7.14.2-5)

## [8.](#section-8) [Native Applications](#name-native-applications)

Native applications are clients installed and executed on the device used by the resource owner (i.e., desktop applications or native mobile applications). Native applications require special consideration related to security, platform capabilities, and overall end-user experience.[¶](#section-8-1)

The guidance in this section is primarily in the context of native mobile apps as opposed to desktop apps. The native mobile platforms have matured more than the desktop platforms in terms of the capabilities provided to app developers relevant to the OAuth flows described here. While the guidance is primarily focused on mobile apps, much of it generally can apply to desktop apps as well.[¶](#section-8-2)

The authorization endpoint requires interaction between the client and the resource owner's user agent. The best current practice is to perform the OAuth authorization request in an external user agent (typically the browser) rather than an embedded user agent (such as one implemented with web-views).[¶](#section-8-3)

The native application can capture the response from the authorization server in several different ways with differing security properties of each. For example, using a redirect URI with an "app-claimed URL" or custom URL scheme registered with the operating system to invoke the client as the handler, manual copy-and-paste of the credentials, running a local web server, installing a user agent extension, or by providing a redirect URI identifying a server-hosted resource under the client's control, which in turn makes the response available to the native application.[¶](#section-8-4)

Previously, it was common for native apps to use embedded user agents (commonly implemented with web-views) for OAuth authorization requests. That approach has many drawbacks, including the host app being able to copy user credentials and cookies as well as the user needing to authenticate from scratch in each app. See [Section 8.5.1](#native-apps-embedded-user-agents) for a deeper analysis of the drawbacks of using embedded user agents for OAuth.[¶](#section-8-5)

Native app authorization requests that use the system browser are more secure and can take advantage of the user's authentication state on the device. Being able to use the existing authentication session in the browser enables single sign-on, as users don't need to authenticate to the authorization server each time they use a new app (unless required by the authorization server policy).[¶](#section-8-6)

Supporting authorization flows between a native app and the browser is possible without changing the OAuth protocol itself, as the OAuth authorization request and response are already defined in terms of URIs. This encompasses URIs that can be used for inter-app communication. Some OAuth server implementations that assume all clients are confidential web clients will need to add an understanding of public native app clients and the types of redirect URIs they use to support this best practice.[¶](#section-8-7)

### [8.1.](#section-8.1) [Client Authentication of Native Apps](#name-client-authentication-of-na)

Secrets that are statically included as part of an app distributed to multiple users are not confidential secrets, as one user may inspect their copy and learn the shared secret. For this reason, authorization servers MUST NOT require client authentication of native app clients using a shared secret, as this serves no value beyond client identification which is already provided by the `client_id` request parameter.[¶](#section-8.1-1)

Authorization servers that still require a statically included shared secret for native app clients MUST treat the client as a public client (as defined in [Section 2.1](#client-types)), and not accept the secret as proof of the client's identity. Without additional measures, such clients are subject to client impersonation (see [Section 7.3.1](#native-app-client-impersonation)).[¶](#section-8.1-2)

#### [8.1.1.](#section-8.1.1) [Registration of Native App Clients](#name-registration-of-native-app-)

Except when using a mechanism like Dynamic Client Registration \[[RFC7591](#RFC7591)] to provision per-instance credentials, native apps are classified as public clients, as defined in [Section 2.1](#client-types), and MUST be registered with the authorization server as such. Authorization servers MUST record the client type in the client registration details in order to identify and process requests accordingly.[¶](#section-8.1.1-1)

#### [8.1.2.](#section-8.1.2) [Native App Attestation](#name-native-app-attestation)

The draft specification \[[I-D.ietf-oauth-attestation-based-client-auth](#I-D.ietf-oauth-attestation-based-client-auth)] defines a mechanism that can be used by a native app to obtain a key-bound attestation to authenticate to an authorization server or resource server. This can provide a higher level of assurance of a mobile app's identity.[¶](#section-8.1.2-1)

### [8.2.](#section-8.2) [Using Inter-App URI Communication for OAuth in Native Apps](#name-using-inter-app-uri-communi)

Just as URIs are used for OAuth on the web to initiate the authorization request and return the authorization response to the requesting website, URIs can be used by native apps to initiate the authorization request in the device's browser and return the response to the requesting native app.[¶](#section-8.2-1)

By adopting the same methods used on the web for OAuth, benefits seen in the web context like the usability of a single sign-on session and the security of a separate authentication context are likewise gained in the native app context. Reusing the same approach also reduces the implementation complexity and increases interoperability by relying on standards-based web flows that are not specific to a particular platform.[¶](#section-8.2-2)

Native apps MUST use an external user agent to perform OAuth authorization requests. This is achieved by opening the authorization request in the browser (detailed in [Section 8.3](#authorization-request-native-app)) and using a redirect URI that will return the authorization response back to the native app (defined in [Section 8.4](#authorization-response-native-app)).[¶](#section-8.2-3)

### [8.3.](#section-8.3) [Initiating the Authorization Request from a Native App](#name-initiating-the-authorizatio)

Native apps needing user authorization create an authorization request URI with the authorization code grant type per [Section 4.1](#authorization-code-grant) using a redirect URI capable of being received by the native app.[¶](#section-8.3-1)

The function of the redirect URI for a native app authorization request is similar to that of a web-based authorization request. Rather than returning the authorization response to the OAuth client's server, the redirect URI used by a native app returns the response to the app. Several options for a redirect URI that will return the authorization response to the native app in different platforms are documented in [Section 8.4](#authorization-response-native-app). Any redirect URI that allows the app to receive the URI and inspect its parameters is viable.[¶](#section-8.3-2)

After constructing the authorization request URI, the app uses platform-specific APIs to open the URI in an external user agent. Typically, the external user agent used is the default browser, that is, the application configured for handling `http` and `https` scheme URIs on the system; however, different browser selection criteria and other categories of external user agents MAY be used.[¶](#section-8.3-3)

This best practice focuses on the browser as the RECOMMENDED external user agent for native apps. An external user agent designed specifically for user authorization and capable of processing authorization requests and responses like a browser MAY also be used. Other external user agents, such as a native app provided by the authorization server may meet the criteria set out in this best practice, including using the same redirect URI properties, but their use is out of scope for this specification.[¶](#section-8.3-4)

Some platforms support a browser feature known as "in-app browser tabs", where an app can present a tab of the browser within the app context without switching apps, but still retain key benefits of the browser such as a shared authentication state and security context. On platforms where they are supported, it is RECOMMENDED, for usability reasons, that apps use in-app browser tabs for the authorization request.[¶](#section-8.3-5)

### [8.4.](#section-8.4) [Receiving the Authorization Response in a Native App](#name-receiving-the-authorization)

There are several redirect URI options available to native apps for receiving the authorization response from the browser, the availability and user experience of which varies by platform.[¶](#section-8.4-1)

#### [8.4.1.](#section-8.4.1) [Claimed "https" Scheme URI Redirection](#name-claimed-https-scheme-uri-re)

Some operating systems, in particular mobile operating systems, allow apps to claim `https` URIs (see [Section 4.2.2](https://rfc-editor.org/rfc/rfc9110#section-4.2.2) of \[[RFC9110](#RFC9110)]) in the domains they control. When the browser encounters a claimed URI, instead of the page being loaded in the browser, the native app is launched with the URI supplied as a launch parameter.[¶](#section-8.4.1-1)

Such URIs can be used as redirect URIs by native apps. They are indistinguishable to the authorization server from a regular web- based client redirect URI. An example is:[¶](#section-8.4.1-2)

```
https://app.example.com/oauth2redirect/example-provider
```

[¶](#section-8.4.1-3)

As the redirect URI alone is not enough to distinguish public native app clients from confidential web clients, it is REQUIRED in [Section 8.1.1](#native-app-registration) that the client type be recorded during client registration to enable the server to determine the client type and act accordingly.[¶](#section-8.4.1-4)

App-claimed `https` scheme redirect URIs have some advantages compared to other native app redirect options in that the identity of the destination app is guaranteed to the authorization server by the operating system. For this reason, native apps SHOULD use them over the other options where possible.[¶](#section-8.4.1-5)

#### [8.4.2.](#section-8.4.2) [Loopback Interface Redirection](#name-loopback-interface-redirect)

Native apps that are able to open a port on the loopback network interface without needing special permissions (typically, those on desktop operating systems) can use the loopback interface to receive the OAuth redirect.[¶](#section-8.4.2-1)

Loopback redirect URIs use the `http` scheme and are constructed with the loopback IP literal and whatever port the client is listening on.[¶](#section-8.4.2-2)

That is, `http://127.0.0.1:{port}/{path}` for IPv4, and `http://[::1]:{port}/{path}` for IPv6. An example redirect using the IPv4 loopback interface with a randomly assigned port:[¶](#section-8.4.2-3)

```
http://127.0.0.1:51004/oauth2redirect/example-provider
```

[¶](#section-8.4.2-4)

An example redirect using the IPv6 loopback interface with a randomly assigned port:[¶](#section-8.4.2-5)

```
http://[::1]:61023/oauth2redirect/example-provider
```

[¶](#section-8.4.2-6)

While redirect URIs using the name `localhost` (i.e., `http://localhost:{port}/{path}`) function similarly to loopback IP redirects, the use of `localhost` is NOT RECOMMENDED. Specifying a redirect URI with the loopback IP literal rather than `localhost` avoids inadvertently listening on network interfaces other than the loopback interface. It is also less susceptible to client-side firewalls and misconfigured host name resolution on the user's device.[¶](#section-8.4.2-7)

The authorization server MUST allow any port to be specified at the time of the request for loopback IP redirect URIs, to accommodate clients that obtain an available ephemeral port from the operating system at the time of the request.[¶](#section-8.4.2-8)

Clients SHOULD NOT assume that the device supports a particular version of the Internet Protocol. It is RECOMMENDED that clients attempt to bind to the loopback interface using both IPv4 and IPv6 and use whichever is available.[¶](#section-8.4.2-9)

#### [8.4.3.](#section-8.4.3) [Private-Use URI Scheme Redirection](#name-private-use-uri-scheme-redi)

Many mobile and desktop computing platforms support inter-app communication via URIs by allowing apps to register private-use URI schemes (sometimes colloquially referred to as "custom URL schemes") like `com.example.app`. When the browser or another app attempts to load a URI with a private-use URI scheme, the app that registered it is launched to handle the request.[¶](#section-8.4.3-1)

Many environments that support private-use URI schemes do not provide a mechanism to claim a scheme and prevent other parties from using another application's scheme. As such, clients using private-use URI schemes are vulnerable to potential attacks on their redirect URIs, so this option should only be used if the previously mentioned more secure options are not available.[¶](#section-8.4.3-2)

To perform an authorization request with a private-use URI scheme redirect, the native app launches the browser with a standard authorization request, but one where the redirect URI utilizes a private-use URI scheme it registered with the operating system.[¶](#section-8.4.3-3)

When choosing a URI scheme to associate with the app, apps MUST use a URI scheme based on a domain name under their control, expressed in reverse order, as recommended by [Section 3.8](https://rfc-editor.org/rfc/rfc7595#section-3.8) of \[[RFC7595](#RFC7595)] for private-use URI schemes.[¶](#section-8.4.3-4)

For example, an app that controls the domain name `app.example.com` can use `com.example.app` as their scheme. Some authorization servers assign client identifiers based on domain names, for example, `client1234.usercontent.example.net`, which can also be used as the domain name for the scheme when reversed in the same manner. A scheme such as `myapp`, however, would not meet this requirement, as it is not based on a domain name.[¶](#section-8.4.3-5)

When there are multiple apps by the same publisher, care must be taken so that each scheme is unique within that group. On platforms that use app identifiers based on reverse-order domain names, those identifiers can be reused as the private-use URI scheme for the OAuth redirect to help avoid this problem.[¶](#section-8.4.3-6)

Following the requirements of [Section 3.2](https://rfc-editor.org/rfc/rfc3986#section-3.2) of \[[RFC3986](#RFC3986)], as there is no naming authority for private-use URI scheme redirects, only a single slash (`/`) appears after the scheme component. A complete example of a redirect URI utilizing a private-use URI scheme is:[¶](#section-8.4.3-7)

```
com.example.app:/oauth2redirect/example-provider
```

[¶](#section-8.4.3-8)

When the authorization server completes the request, it redirects to the client's redirect URI as it would normally. As the redirect URI uses a private-use URI scheme, it results in the operating system launching the native app, passing in the URI as a launch parameter. Then, the native app uses normal processing for the authorization response.[¶](#section-8.4.3-9)

### [8.5.](#section-8.5) [Security Considerations in Native Apps](#name-security-considerations-in-)

#### [8.5.1.](#section-8.5.1) [Embedded User Agents in Native Apps](#name-embedded-user-agents-in-nat)

Embedded user agents are a technically possible method for authorizing native apps. These embedded user agents are unsafe for use by third parties to the authorization server by definition, as the app that hosts the embedded user agent can access the user's full authentication credentials, not just the OAuth authorization grant that was intended for the app. They are also typically sandboxed by the operating system and mechanisms such as WebAuthn that rely on the web origin are disabled.[¶](#section-8.5.1-1)

In typical web-view-based implementations of embedded user agents, the host application can record every keystroke entered in the login form to capture usernames and passwords, automatically submit forms to bypass user consent, and copy session cookies and use them to perform authenticated actions as the user.[¶](#section-8.5.1-2)

Even when used by trusted apps belonging to the same party as the authorization server, embedded user agents violate the principle of least privilege by having access to more powerful credentials than they need, potentially increasing the attack surface.[¶](#section-8.5.1-3)

Encouraging users to enter credentials in an embedded user agent without the usual address bar and visible certificate validation features that browsers have makes it impossible for the user to know if they are signing in to the legitimate site; even when they are, it trains them that it's OK to enter credentials without validating the site first.[¶](#section-8.5.1-4)

Aside from the security concerns, embedded user agents do not share the authentication state with other apps or the browser, requiring the user to log in for every authorization request, which is often considered an inferior user experience.[¶](#section-8.5.1-5)

#### [8.5.2.](#section-8.5.2) [Fake External User-Agents in Native Apps](#name-fake-external-user-agents-i)

The native app that is initiating the authorization request has a large degree of control over the user interface and can potentially present a fake external user agent, that is, an embedded user agent made to appear as an external user agent.[¶](#section-8.5.2-1)

When all good actors are using external user agents, the advantage is that it is possible for security experts to detect bad actors, as anyone faking an external user agent is provably bad. On the other hand, if good and bad actors alike are using embedded user agents, bad actors don't need to fake anything, making them harder to detect. Once a malicious app is detected, it may be possible to use this knowledge to blacklist the app's signature in malware scanning software, take removal action (in the case of apps distributed by app stores) and other steps to reduce the impact and spread of the malicious app.[¶](#section-8.5.2-2)

Authorization servers can also directly protect against fake external user agents by requiring an authentication factor only available to true external user agents.[¶](#section-8.5.2-3)

Users who are particularly concerned about their security when using in-app browser tabs may also take the additional step of opening the request in the full browser from the in-app browser tab and complete the authorization there, as most implementations of the in-app browser tab pattern offer such functionality.[¶](#section-8.5.2-4)

#### [8.5.3.](#section-8.5.3) [Malicious External User-Agents in Native Apps](#name-malicious-external-user-age)

If a malicious app is able to configure itself as the default handler for `https` scheme URIs in the operating system, it will be able to intercept authorization requests that use the default browser and abuse this position of trust for malicious ends such as phishing the user.[¶](#section-8.5.3-1)

This attack is not confined to OAuth; a malicious app configured in this way would present a general and ongoing risk to the user beyond OAuth usage by native apps. Many operating systems mitigate this issue by requiring an explicit user action to change the default handler for `http` and `https` scheme URIs.[¶](#section-8.5.3-2)

#### [8.5.4.](#section-8.5.4) [Loopback Redirect Considerations in Native Apps](#name-loopback-redirect-considera)

Loopback interface redirect URIs MAY use the `http` scheme (i.e., without TLS). This is acceptable for loopback interface redirect URIs as the HTTP request never leaves the device.[¶](#section-8.5.4-1)

Clients SHOULD open the network port only when starting the authorization request and close it once the response is returned.[¶](#section-8.5.4-2)

Clients SHOULD listen on the loopback network interface only, in order to avoid interference by other network actors.[¶](#section-8.5.4-3)

Clients SHOULD use loopback IP literals rather than the string `localhost` as described in [Section 8.4.2](#loopback-interface-redirection).[¶](#section-8.5.4-4)

## [9.](#section-9) [Browser-Based Apps](#name-browser-based-apps)

Browser-based apps are clients that run in a web browser, typically written in JavaScript, also known as "single-page apps". These types of apps have particular security considerations similar to native apps.[¶](#section-9-1)

TODO: Bring in the normative text of the browser-based apps BCP when it is finalized.[¶](#section-9-2)

## [10.](#section-10) [Differences from OAuth 2.0](#name-differences-from-oauth-20)

This draft consolidates the functionality in OAuth 2.0 \[[RFC6749](#RFC6749)], OAuth 2.0 for Native Apps \[[RFC8252](#RFC8252)], Proof Key for Code Exchange \[[RFC7636](#RFC7636)], OAuth 2.0 for Browser-Based Apps \[[I-D.ietf-oauth-browser-based-apps](#I-D.ietf-oauth-browser-based-apps)], OAuth Security Best Current Practice \[[RFC9700](#RFC9700)], and Bearer Token Usage \[[RFC6750](#RFC6750)].[¶](#section-10-1)

Where a later draft updates or obsoletes functionality found in the original \[[RFC6749](#RFC6749)], that functionality in this draft is updated with the normative changes described in a later draft, or removed entirely.[¶](#section-10-2)

A non-normative list of changes from OAuth 2.0 is listed below:[¶](#section-10-3)

- The authorization code grant is extended with the functionality from PKCE \[[RFC7636](#RFC7636)] such that the default method of using the authorization code grant according to this specification requires the addition of the PKCE parameters[¶](#section-10-4.1.1)
- Redirect URIs must be compared using exact string matching as per [Section 4.1.3](https://rfc-editor.org/rfc/rfc9700#section-4.1.3) of \[[RFC9700](#RFC9700)][¶](#section-10-4.2.1)
- The Implicit grant (`response_type=token`) is omitted from this specification as per [Section 2.1.2](https://rfc-editor.org/rfc/rfc9700#section-2.1.2) of \[[RFC9700](#RFC9700)][¶](#section-10-4.3.1)
- The Resource Owner Password Credentials grant is omitted from this specification as per [Section 2.4](https://rfc-editor.org/rfc/rfc9700#section-2.4) of \[[RFC9700](#RFC9700)][¶](#section-10-4.4.1)
- Bearer token usage omits the use of bearer tokens in the query string of URIs as per [Section 4.3.2](https://rfc-editor.org/rfc/rfc9700#section-4.3.2) of \[[RFC9700](#RFC9700)][¶](#section-10-4.5.1)
- Refresh tokens for public clients must either be sender-constrained or one-time use as per [Section 4.14.2](https://rfc-editor.org/rfc/rfc9700#section-4.14.2) of \[[RFC9700](#RFC9700)][¶](#section-10-4.6.1)
- The token endpoint request containing an authorization code no longer contains the `redirect_uri` parameter[¶](#section-10-4.7.1)
- Authorization servers must support client credentials in the request body[¶](#section-10-4.8.1)

### [10.1.](#section-10.1) [Removal of the OAuth 2.0 Implicit grant](#name-removal-of-the-oauth-20-imp)

The OAuth 2.0 Implicit grant is omitted from OAuth 2.1 as it was deprecated in \[[RFC9700](#RFC9700)].[¶](#section-10.1-1)

The intent of removing the Implicit grant is to no longer issue access tokens in the authorization response, as such tokens are vulnerable to leakage and injection, and are unable to be sender-constrained to a client. This behavior was indicated by clients using the `response_type=token` parameter. This value for the `response_type` parameter is no longer defined in OAuth 2.1.[¶](#section-10.1-2)

Removal of `response_type=token` does not have an effect on other extension response types returning other artifacts from the authorization endpoint, for example, `response_type=id_token` defined by \[[OpenID.Connect](#OpenID.Connect)].[¶](#section-10.1-3)

### [10.2.](#section-10.2) [Redirect URI Parameter in Token Request](#name-redirect-uri-parameter-in-t)

In OAuth 2.0, the request to the token endpoint in the authorization code flow ([Section 4.1.3](https://rfc-editor.org/rfc/rfc6749#section-4.1.3) of \[[RFC6749](#RFC6749)]) contains an optional `redirect_uri` parameter. The parameter was intended to prevent an authorization code injection attack, and was required if the `redirect_uri` parameter was sent in the original authorization request. The authorization request only required the `redirect_uri` parameter if multiple redirect URIs were registered to the specific client. However, in practice, many authorization server implementations required the `redirect_uri` parameter in the authorization request even if only one was registered, leading the `redirect_uri` parameter to be required at the token endpoint as well.[¶](#section-10.2-1)

In OAuth 2.1, authorization code injection is prevented by the `code_challenge` and `code_verifier` parameters, making the inclusion of the `redirect_uri` parameter serve no purpose in the token request. As such, it has been removed.[¶](#section-10.2-2)

For backwards compatibility of an authorization server wishing to support both OAuth 2.0 and OAuth 2.1 clients, the authorization server MUST allow clients to send the `redirect_uri` parameter in the token request ([Section 4.1.3](#code-token-extension)), and MUST enforce the parameter as described in \[[RFC6749](#RFC6749)]. The authorization server can use the `client_id` in the request to determine whether to enforce this behavior for the specific client that it knows will be using the older OAuth 2.0 behavior.[¶](#section-10.2-3)

A client following only the OAuth 2.1 recommendations will not send the `redirect_uri` in the token request, and therefore will not be compatible with an authorization server that expects the parameter in the token request.[¶](#section-10.2-4)

## [11.](#section-11) [IANA Considerations](#name-iana-considerations)

This document does not require any IANA actions.[¶](#section-11-1)

All referenced registries are defined by \[[RFC6749](#RFC6749)] and related documents that this work is based upon. No changes to those registries are required by this specification.[¶](#section-11-2)

## [12.](#section-12) [References](#name-references)

### [12.1.](#section-12.1) [Normative References](#name-normative-references)

\[BCP195]

Saint-Andre, P., "Recommendations for Secure Use of Transport Layer Security (TLS)", 2015.

\[RFC2119]

Bradner, S., "Key words for use in RFCs to Indicate Requirement Levels", BCP 14, RFC 2119, DOI 10.17487/RFC2119, March 1997, &lt;[https://www.rfc-editor.org/info/rfc2119](https://www.rfc-editor.org/info/rfc2119)&gt;.

\[RFC2617]

Franks, J., Hallam-Baker, P., Hostetler, J., Lawrence, S., Leach, P., Luotonen, A., and L. Stewart, "HTTP Authentication: Basic and Digest Access Authentication", RFC 2617, DOI 10.17487/RFC2617, June 1999, &lt;[https://www.rfc-editor.org/info/rfc2617](https://www.rfc-editor.org/info/rfc2617)&gt;.

\[RFC3629]

Yergeau, F., "UTF-8, a transformation format of ISO 10646", STD 63, RFC 3629, DOI 10.17487/RFC3629, November 2003, &lt;[https://www.rfc-editor.org/info/rfc3629](https://www.rfc-editor.org/info/rfc3629)&gt;.

\[RFC3986]

Berners-Lee, T., Fielding, R., and L. Masinter, "Uniform Resource Identifier (URI): Generic Syntax", STD 66, RFC 3986, DOI 10.17487/RFC3986, January 2005, &lt;[https://www.rfc-editor.org/info/rfc3986](https://www.rfc-editor.org/info/rfc3986)&gt;.

\[RFC4949]

Shirey, R., "Internet Security Glossary, Version 2", FYI 36, RFC 4949, DOI 10.17487/RFC4949, August 2007, &lt;[https://www.rfc-editor.org/info/rfc4949](https://www.rfc-editor.org/info/rfc4949)&gt;.

\[RFC5234]

Crocker, D., Ed. and P. Overell, "Augmented BNF for Syntax Specifications: ABNF", STD 68, RFC 5234, DOI 10.17487/RFC5234, January 2008, &lt;[https://www.rfc-editor.org/info/rfc5234](https://www.rfc-editor.org/info/rfc5234)&gt;.

\[RFC6749]

Hardt, D., Ed., "The OAuth 2.0 Authorization Framework", RFC 6749, DOI 10.17487/RFC6749, October 2012, &lt;[https://www.rfc-editor.org/info/rfc6749](https://www.rfc-editor.org/info/rfc6749)&gt;.

\[RFC6750]

Jones, M. and D. Hardt, "The OAuth 2.0 Authorization Framework: Bearer Token Usage", RFC 6750, DOI 10.17487/RFC6750, October 2012, &lt;[https://www.rfc-editor.org/info/rfc6750](https://www.rfc-editor.org/info/rfc6750)&gt;.

\[RFC7235]

Fielding, R., Ed. and J. Reschke, Ed., "Hypertext Transfer Protocol (HTTP/1.1): Authentication", RFC 7235, DOI 10.17487/RFC7235, June 2014, &lt;[https://www.rfc-editor.org/info/rfc7235](https://www.rfc-editor.org/info/rfc7235)&gt;.

\[RFC7521]

Campbell, B., Mortimore, C., Jones, M., and Y. Goland, "Assertion Framework for OAuth 2.0 Client Authentication and Authorization Grants", RFC 7521, DOI 10.17487/RFC7521, May 2015, &lt;[https://www.rfc-editor.org/info/rfc7521](https://www.rfc-editor.org/info/rfc7521)&gt;.

\[RFC7523]

Jones, M., Campbell, B., and C. Mortimore, "JSON Web Token (JWT) Profile for OAuth 2.0 Client Authentication and Authorization Grants", RFC 7523, DOI 10.17487/RFC7523, May 2015, &lt;[https://www.rfc-editor.org/info/rfc7523](https://www.rfc-editor.org/info/rfc7523)&gt;.

\[RFC7595]

Thaler, D., Ed., Hansen, T., and T. Hardie, "Guidelines and Registration Procedures for URI Schemes", BCP 35, RFC 7595, DOI 10.17487/RFC7595, June 2015, &lt;[https://www.rfc-editor.org/info/rfc7595](https://www.rfc-editor.org/info/rfc7595)&gt;.

\[RFC8174]

Leiba, B., "Ambiguity of Uppercase vs Lowercase in RFC 2119 Key Words", BCP 14, RFC 8174, DOI 10.17487/RFC8174, May 2017, &lt;[https://www.rfc-editor.org/info/rfc8174](https://www.rfc-editor.org/info/rfc8174)&gt;.

\[RFC8252]

Denniss, W. and J. Bradley, "OAuth 2.0 for Native Apps", BCP 212, RFC 8252, DOI 10.17487/RFC8252, October 2017, &lt;[https://www.rfc-editor.org/info/rfc8252](https://www.rfc-editor.org/info/rfc8252)&gt;.

\[RFC8259]

Bray, T., Ed., "The JavaScript Object Notation (JSON) Data Interchange Format", STD 90, RFC 8259, DOI 10.17487/RFC8259, December 2017, &lt;[https://www.rfc-editor.org/info/rfc8259](https://www.rfc-editor.org/info/rfc8259)&gt;.

\[RFC8446]

Rescorla, E., "The Transport Layer Security (TLS) Protocol Version 1.3", RFC 8446, DOI 10.17487/RFC8446, August 2018, &lt;[https://www.rfc-editor.org/info/rfc8446](https://www.rfc-editor.org/info/rfc8446)&gt;.

\[RFC9110]

Fielding, R., Ed., Nottingham, M., Ed., and J. Reschke, Ed., "HTTP Semantics", STD 97, RFC 9110, DOI 10.17487/RFC9110, June 2022, &lt;[https://www.rfc-editor.org/info/rfc9110](https://www.rfc-editor.org/info/rfc9110)&gt;.

\[RFC9111]

Fielding, R., Ed., Nottingham, M., Ed., and J. Reschke, Ed., "HTTP Caching", STD 98, RFC 9111, DOI 10.17487/RFC9111, June 2022, &lt;[https://www.rfc-editor.org/info/rfc9111](https://www.rfc-editor.org/info/rfc9111)&gt;.

\[RFC9207]

Meyer zu Selhausen, K. and D. Fett, "OAuth 2.0 Authorization Server Issuer Identification", RFC 9207, DOI 10.17487/RFC9207, March 2022, &lt;[https://www.rfc-editor.org/info/rfc9207](https://www.rfc-editor.org/info/rfc9207)&gt;.

\[RFC9700]

Lodderstedt, T., Bradley, J., Labunets, A., and D. Fett, "Best Current Practice for OAuth 2.0 Security", BCP 240, RFC 9700, DOI 10.17487/RFC9700, January 2025, &lt;[https://www.rfc-editor.org/info/rfc9700](https://www.rfc-editor.org/info/rfc9700)&gt;.

\[USASCII]

Institute, A. N. S., "Coded Character Set -- 7-bit American Standard Code for Information Interchange, ANSI X3.4", 1986.

\[W3C.REC-xml-20081126]

Bray, T., Paoli, J., Sperberg-McQueen, C. M., Maler, E., and F. Yergeau, "Extensible Markup Language", November 2008, &lt;[https://www.w3.org/TR/REC-xml/REC-xml-20081126.xml](https://www.w3.org/TR/REC-xml/REC-xml-20081126.xml)&gt;.

\[WHATWG.CORS]

WHATWG, "Fetch Standard: CORS protocol", June 2023, &lt;[https://fetch.spec.whatwg.org/#http-cors-protocol](https://fetch.spec.whatwg.org/#http-cors-protocol)&gt;.

\[WHATWG.URL]

WHATWG, "URL", May 2022, &lt;[https://url.spec.whatwg.org/](https://url.spec.whatwg.org/)&gt;.

### [12.2.](#section-12.2) [Informative References](#name-informative-references)

\[CSP-2]

"Content Security Policy Level 2", December 2016, &lt;[https://www.w3.org/TR/CSP2](https://www.w3.org/TR/CSP2)&gt;.

\[I-D.bradley-oauth-jwt-encoded-state]

Bradley, J., Lodderstedt, T., and H. Zandbelt, "Encoding claims in the OAuth 2 state parameter using a JWT", Work in Progress, Internet-Draft, draft-bradley-oauth-jwt-encoded-state-09, 4 November 2018, &lt;[https://datatracker.ietf.org/doc/html/draft-bradley-oauth-jwt-encoded-state-09](https://datatracker.ietf.org/doc/html/draft-bradley-oauth-jwt-encoded-state-09)&gt;.

\[I-D.ietf-oauth-attestation-based-client-auth]

Looker, T., Bastian, P., and C. Bormann, "OAuth 2.0 Attestation-Based Client Authentication", Work in Progress, Internet-Draft, draft-ietf-oauth-attestation-based-client-auth-07, 15 September 2025, &lt;[https://datatracker.ietf.org/doc/html/draft-ietf-oauth-attestation-based-client-auth-07](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-attestation-based-client-auth-07)&gt;.

\[I-D.ietf-oauth-browser-based-apps]

Parecki, A., De Ryck, P., and D. Waite, "OAuth 2.0 for Browser-Based Applications", Work in Progress, Internet-Draft, draft-ietf-oauth-browser-based-apps-26, 3 December 2025, &lt;[https://datatracker.ietf.org/doc/html/draft-ietf-oauth-browser-based-apps-26](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-browser-based-apps-26)&gt;.

\[I-D.ietf-oauth-rfc7523bis]

Jones, M. B., Campbell, B., Mortimore, C., and F. Skokan, "Updates to OAuth 2.0 JSON Web Token (JWT) Client Authentication and Assertion-Based Authorization Grants", Work in Progress, Internet-Draft, draft-ietf-oauth-rfc7523bis-05, 12 January 2026, &lt;[https://datatracker.ietf.org/doc/html/draft-ietf-oauth-rfc7523bis-05](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-rfc7523bis-05)&gt;.

\[NIST800-63]

Burr, W., Dodson, D., Newton, E., Perlner, R., Polk, T., Gupta, S., and E. Nabbus, "NIST Special Publication 800-63-1, INFORMATION SECURITY", December 2011, &lt;[http://csrc.nist.gov/publications/](http://csrc.nist.gov/publications/)&gt;.

\[OMAP]

Huff, J., Schlacht, D., Nadalin, A., Simmons, J., Rosenberg, P., Madsen, P., Ace, T., Rickelton-Abdi, C., and B. Boyer, "Online Multimedia Authorization Protocol: An Industry Standard for Authorized Access to Internet Multimedia Resources", August 2012, &lt;[https://www.svta.org/product/online-multimedia-authorization-protocol/](https://www.svta.org/product/online-multimedia-authorization-protocol/)&gt;.

\[OpenID.Connect]

Sakimura, N., Bradley, J., Jones, M., de Medeiros, B., and C. Mortimore, "OpenID Connect Core 1.0 incorporating errata set 2", December 2023, &lt;[https://openid.net/specs/openid-connect-core-1\_0.html](https://openid.net/specs/openid-connect-core-1_0.html)&gt;.

\[OpenID.Discovery]

Sakimura, N., Bradley, J., Jones, M., and E. Jay, "OpenID Connect Discovery 1.0 incorporating errata set 2", December 2023, &lt;[https://openid.net/specs/openid-connect-discovery-1\_0.html](https://openid.net/specs/openid-connect-discovery-1_0.html)&gt;.

\[OpenID.Messages]

Sakimura, N., Bradley, J., Jones, M., de Medeiros, B., Mortimore, C., and E. Jay, "OpenID Connect Messages 1.0", June 2012, &lt;[http://openid.net/specs/openid-connect-messages-1\_0.html](http://openid.net/specs/openid-connect-messages-1_0.html)&gt;.

\[owasp\_redir]

"OWASP Cheat Sheet Series - Unvalidated Redirects and Forwards", 2020, &lt;[https://cheatsheetseries.owasp.org/cheatsheets/Unvalidated\_Redirects\_and\_Forwards\_Cheat\_Sheet.html](https://cheatsheetseries.owasp.org/cheatsheets/Unvalidated_Redirects_and_Forwards_Cheat_Sheet.html)&gt;.

\[RFC6265]

Barth, A., "HTTP State Management Mechanism", RFC 6265, DOI 10.17487/RFC6265, April 2011, &lt;[https://www.rfc-editor.org/info/rfc6265](https://www.rfc-editor.org/info/rfc6265)&gt;.

\[RFC6819]

Lodderstedt, T., Ed., McGloin, M., and P. Hunt, "OAuth 2.0 Threat Model and Security Considerations", RFC 6819, DOI 10.17487/RFC6819, January 2013, &lt;[https://www.rfc-editor.org/info/rfc6819](https://www.rfc-editor.org/info/rfc6819)&gt;.

\[RFC7009]

Lodderstedt, T., Ed., Dronia, S., and M. Scurtescu, "OAuth 2.0 Token Revocation", RFC 7009, DOI 10.17487/RFC7009, August 2013, &lt;[https://www.rfc-editor.org/info/rfc7009](https://www.rfc-editor.org/info/rfc7009)&gt;.

\[RFC7519]

Jones, M., Bradley, J., and N. Sakimura, "JSON Web Token (JWT)", RFC 7519, DOI 10.17487/RFC7519, May 2015, &lt;[https://www.rfc-editor.org/info/rfc7519](https://www.rfc-editor.org/info/rfc7519)&gt;.

\[RFC7591]

Richer, J., Ed., Jones, M., Bradley, J., Machulak, M., and P. Hunt, "OAuth 2.0 Dynamic Client Registration Protocol", RFC 7591, DOI 10.17487/RFC7591, July 2015, &lt;[https://www.rfc-editor.org/info/rfc7591](https://www.rfc-editor.org/info/rfc7591)&gt;.

\[RFC7592]

Richer, J., Ed., Jones, M., Bradley, J., and M. Machulak, "OAuth 2.0 Dynamic Client Registration Management Protocol", RFC 7592, DOI 10.17487/RFC7592, July 2015, &lt;[https://www.rfc-editor.org/info/rfc7592](https://www.rfc-editor.org/info/rfc7592)&gt;.

\[RFC7636]

Sakimura, N., Ed., Bradley, J., and N. Agarwal, "Proof Key for Code Exchange by OAuth Public Clients", RFC 7636, DOI 10.17487/RFC7636, September 2015, &lt;[https://www.rfc-editor.org/info/rfc7636](https://www.rfc-editor.org/info/rfc7636)&gt;.

\[RFC7662]

Richer, J., Ed., "OAuth 2.0 Token Introspection", RFC 7662, DOI 10.17487/RFC7662, October 2015, &lt;[https://www.rfc-editor.org/info/rfc7662](https://www.rfc-editor.org/info/rfc7662)&gt;.

\[RFC8414]

Jones, M., Sakimura, N., and J. Bradley, "OAuth 2.0 Authorization Server Metadata", RFC 8414, DOI 10.17487/RFC8414, June 2018, &lt;[https://www.rfc-editor.org/info/rfc8414](https://www.rfc-editor.org/info/rfc8414)&gt;.

\[RFC8628]

Denniss, W., Bradley, J., Jones, M., and H. Tschofenig, "OAuth 2.0 Device Authorization Grant", RFC 8628, DOI 10.17487/RFC8628, August 2019, &lt;[https://www.rfc-editor.org/info/rfc8628](https://www.rfc-editor.org/info/rfc8628)&gt;.

\[RFC8705]

Campbell, B., Bradley, J., Sakimura, N., and T. Lodderstedt, "OAuth 2.0 Mutual-TLS Client Authentication and Certificate-Bound Access Tokens", RFC 8705, DOI 10.17487/RFC8705, February 2020, &lt;[https://www.rfc-editor.org/info/rfc8705](https://www.rfc-editor.org/info/rfc8705)&gt;.

\[RFC8707]

Campbell, B., Bradley, J., and H. Tschofenig, "Resource Indicators for OAuth 2.0", RFC 8707, DOI 10.17487/RFC8707, February 2020, &lt;[https://www.rfc-editor.org/info/rfc8707](https://www.rfc-editor.org/info/rfc8707)&gt;.

\[RFC9068]

Bertocci, V., "JSON Web Token (JWT) Profile for OAuth 2.0 Access Tokens", RFC 9068, DOI 10.17487/RFC9068, October 2021, &lt;[https://www.rfc-editor.org/info/rfc9068](https://www.rfc-editor.org/info/rfc9068)&gt;.

\[RFC9126]

Lodderstedt, T., Campbell, B., Sakimura, N., Tonge, D., and F. Skokan, "OAuth 2.0 Pushed Authorization Requests", RFC 9126, DOI 10.17487/RFC9126, September 2021, &lt;[https://www.rfc-editor.org/info/rfc9126](https://www.rfc-editor.org/info/rfc9126)&gt;.

\[RFC9396]

Lodderstedt, T., Richer, J., and B. Campbell, "OAuth 2.0 Rich Authorization Requests", RFC 9396, DOI 10.17487/RFC9396, May 2023, &lt;[https://www.rfc-editor.org/info/rfc9396](https://www.rfc-editor.org/info/rfc9396)&gt;.

\[RFC9449]

Fett, D., Campbell, B., Bradley, J., Lodderstedt, T., Jones, M., and D. Waite, "OAuth 2.0 Demonstrating Proof of Possession (DPoP)", RFC 9449, DOI 10.17487/RFC9449, September 2023, &lt;[https://www.rfc-editor.org/info/rfc9449](https://www.rfc-editor.org/info/rfc9449)&gt;.

\[RFC9470]

Bertocci, V. and B. Campbell, "OAuth 2.0 Step Up Authentication Challenge Protocol", RFC 9470, DOI 10.17487/RFC9470, September 2023, &lt;[https://www.rfc-editor.org/info/rfc9470](https://www.rfc-editor.org/info/rfc9470)&gt;.

\[W3C.REC-html401-19991224]

Hors, A. L., Ed., Raggett, D., Ed., and I. Jacobs, Ed., "HTML 4.01 Specification", W3C REC REC-html401-19991224, W3C REC-html401-19991224, 24 December 1999, &lt;[https://www.w3.org/TR/1999/REC-html401-19991224/](https://www.w3.org/TR/1999/REC-html401-19991224/)&gt;.

## [Appendix A.](#appendix-A) [Augmented Backus-Naur Form (ABNF) Syntax](#name-augmented-backus-naur-form-)

This section provides Augmented Backus-Naur Form (ABNF) syntax descriptions for the elements defined in this specification using the notation of \[[RFC5234](#RFC5234)]. The ABNF below is defined in terms of Unicode code points \[[W3C.REC-xml-20081126](#W3C.REC-xml-20081126)]; these characters are typically encoded in UTF-8. Elements are presented in the order first defined.[¶](#appendix-A-1)

Some of the definitions that follow use the "URI-reference" definition from \[[RFC3986](#RFC3986)].[¶](#appendix-A-2)

Some of the definitions that follow use these common definitions:[¶](#appendix-A-3)

```
VSCHAR     = %x20-7E
NQCHAR     = %x21 / %x23-5B / %x5D-7E
NQSCHAR    = %x20-21 / %x23-5B / %x5D-7E
```

[¶](#appendix-A-4)

### [A.1.](#appendix-A.1) ["client\_id" Syntax](#name-client_id-syntax)

The `client_id` element is defined in [Section 2.4.1](#client-secret):[¶](#appendix-A.1-1)

### [A.2.](#appendix-A.2) ["client\_secret" Syntax](#name-client_secret-syntax)

The `client_secret` element is defined in [Section 2.4.1](#client-secret):[¶](#appendix-A.2-1)

```
client-secret = *VSCHAR
```

[¶](#appendix-A.2-2)

### [A.3.](#appendix-A.3) ["response\_type" Syntax](#name-response_type-syntax)

The `response_type` element is defined in [Section 4.1.1](#authorization-request) and [Section 6.4](#new-response-types):[¶](#appendix-A.3-1)

```
response-type = response-name *( SP response-name )
response-name = 1*response-char
response-char = "_" / DIGIT / ALPHA
```

[¶](#appendix-A.3-2)

### [A.4.](#appendix-A.4) ["scope" Syntax](#name-scope-syntax)

The `scope` element is defined in [Section 1.4.1](#access-token-scope):[¶](#appendix-A.4-1)

```
 scope       = scope-token *( SP scope-token )
 scope-token = 1*NQCHAR
```

[¶](#appendix-A.4-2)

### [A.5.](#appendix-A.5) ["state" Syntax](#name-state-syntax)

The `state` element is defined in [Section 4.1.1](#authorization-request), [Section 4.1.2](#authorization-response), and [Section 4.1.2.1](#authorization-code-error-response):[¶](#appendix-A.5-1)

### [A.6.](#appendix-A.6) ["redirect\_uri" Syntax](#name-redirect_uri-syntax)

The `redirect_uri` element is defined in [Section 4.1.1](#authorization-request), and [Section 4.1.3](#code-token-extension):[¶](#appendix-A.6-1)

```
 redirect-uri      = URI-reference
```

[¶](#appendix-A.6-2)

### [A.7.](#appendix-A.7) ["error" Syntax](#name-error-syntax)

The `error` element is defined in Sections [Section 4.1.2.1](#authorization-code-error-response), [Section 3.2.4](#token-error-response), and [Section 5.3](#error-response):[¶](#appendix-A.7-1)

### [A.8.](#appendix-A.8) ["error\_description" Syntax](#name-error_description-syntax)

The `error_description` element is defined in Sections [Section 4.1.2.1](#authorization-code-error-response), [Section 3.2.4](#token-error-response), and [Section 5.3](#error-response):[¶](#appendix-A.8-1)

```
 error-description = 1*NQSCHAR
```

[¶](#appendix-A.8-2)

### [A.9.](#appendix-A.9) ["error\_uri" Syntax](#name-error_uri-syntax)

The `error_uri` element is defined in Sections [Section 4.1.2.1](#authorization-code-error-response), [Section 3.2.4](#token-error-response), and [Section 5.3](#error-response):[¶](#appendix-A.9-1)

```
 error-uri         = URI-reference
```

[¶](#appendix-A.9-2)

### [A.10.](#appendix-A.10) ["grant\_type" Syntax](#name-grant_type-syntax)

The `grant_type` element is defined in Section [Section 3.2.2](#token-request):[¶](#appendix-A.10-1)

```
 grant-type = grant-name / URI-reference
 grant-name = 1*name-char
 name-char  = "-" / "." / "_" / DIGIT / ALPHA
```

[¶](#appendix-A.10-2)

### [A.11.](#appendix-A.11) ["code" Syntax](#name-code-syntax)

The `code` element is defined in [Section 4.1.3](#code-token-extension):[¶](#appendix-A.11-1)

### [A.12.](#appendix-A.12) ["access\_token" Syntax](#name-access_token-syntax)

The `access_token` element is defined in [Section 3.2.3](#token-response):[¶](#appendix-A.12-1)

```
 access-token = 1*VSCHAR
```

[¶](#appendix-A.12-2)

### [A.13.](#appendix-A.13) ["token\_type" Syntax](#name-token_type-syntax)

The `token_type` element is defined in [Section 3.2.3](#token-response), and [Section 6.1](#defining-access-token-types):[¶](#appendix-A.13-1)

```
 token-type = type-name / URI-reference
 type-name  = 1*name-char
 name-char  = "-" / "." / "_" / DIGIT / ALPHA
```

[¶](#appendix-A.13-2)

### [A.14.](#appendix-A.14) ["expires\_in" Syntax](#name-expires_in-syntax)

The `expires_in` element is defined in [Section 3.2.3](#token-response):[¶](#appendix-A.14-1)

### [A.15.](#appendix-A.15) ["refresh\_token" Syntax](#name-refresh_token-syntax)

The `refresh_token` element is defined in [Section 3.2.3](#token-response) and [Section 4.3](#refreshing-an-access-token):[¶](#appendix-A.15-1)

```
 refresh-token = 1*VSCHAR
```

[¶](#appendix-A.15-2)

### [A.16.](#appendix-A.16) [Endpoint Parameter Syntax](#name-endpoint-parameter-syntax)

The syntax for new endpoint parameters is defined in [Section 6.2](#defining-new-endpoint-parameters):[¶](#appendix-A.16-1)

```
 param-name = 1*name-char
 name-char  = "-" / "." / "_" / DIGIT / ALPHA
```

[¶](#appendix-A.16-2)

### [A.17.](#appendix-A.17) ["code\_verifier" Syntax](#name-code_verifier-syntax)

ABNF for `code_verifier` is as follows.[¶](#appendix-A.17-1)

```
code-verifier = 43*128unreserved
unreserved = ALPHA / DIGIT / "-" / "." / "_" / "~"
ALPHA = %x41-5A / %x61-7A
DIGIT = %x30-39
```

[¶](#appendix-A.17-2)

### [A.18.](#appendix-A.18) ["code\_challenge" Syntax](#name-code_challenge-syntax)

ABNF for `code_challenge` is as follows.[¶](#appendix-A.18-1)

```
code-challenge = 43*128unreserved
unreserved = ALPHA / DIGIT / "-" / "." / "_" / "~"
ALPHA = %x41-5A / %x61-7A
DIGIT = %x30-39
```

[¶](#appendix-A.18-2)

## [Appendix B.](#appendix-B) [Use of application/x-www-form-urlencoded Media Type](#name-use-of-application-x-www-fo)

At the time of publication of \[[RFC6749](#RFC6749)], the `application/x-www-form-urlencoded` media type was defined in Section 17.13.4 of \[[W3C.REC-html401-19991224](#W3C.REC-html401-19991224)] but not registered in the IANA MIME Media Types registry ([http://www.iana.org/assignments/media-types](http://www.iana.org/assignments/media-types)). Furthermore, that definition is incomplete, as it does not consider non-US-ASCII characters.[¶](#appendix-B-1)

To address this shortcoming when generating contents using this media type, names and values MUST be encoded using the UTF-8 character encoding scheme \[[RFC3629](#RFC3629)] first; the resulting octet sequence then needs to be further encoded using the escaping rules defined in \[[W3C.REC-html401-19991224](#W3C.REC-html401-19991224)].[¶](#appendix-B-2)

When parsing data from a content using this media type, the names and values resulting from reversing the name/value encoding consequently need to be treated as octet sequences, to be decoded using the UTF-8 character encoding scheme.[¶](#appendix-B-3)

For example, the value consisting of the six Unicode code points (1) U+0020 (SPACE), (2) U+0025 (PERCENT SIGN), (3) U+0026 (AMPERSAND), (4) U+002B (PLUS SIGN), (5) U+00A3 (POUND SIGN), and (6) U+20AC (EURO SIGN) would be encoded into the octet sequence below (using hexadecimal notation):[¶](#appendix-B-4)

```
20 25 26 2B C2 A3 E2 82 AC
```

[¶](#appendix-B-5)

and then represented in the content as:[¶](#appendix-B-6)

```
+%25%26%2B%C2%A3%E2%82%AC
```

[¶](#appendix-B-7)

## [Appendix C.](#appendix-C) [Serializations](#name-serializations)

Various messages in this specification are serialized using one of the methods described below. This section describes the syntax of these serialization methods; other sections describe when they can and must be used. Note that not all methods can be used for all messages.[¶](#appendix-C-1)

### [C.1.](#appendix-C.1) [Query String Serialization](#name-query-string-serialization)

In order to serialize the parameters using the Query String Serialization, the Client constructs the string by adding the parameters and values to the query component of a URL using the application/x-www-form-urlencoded format as defined by \[[WHATWG.URL](#WHATWG.URL)]. Query String Serialization is typically used in HTTP GET requests.[¶](#appendix-C.1-1)

### [C.2.](#appendix-C.2) [Form-Encoded Serialization](#name-form-encoded-serialization)

Parameters and their values are Form Serialized by adding the parameter names and values to the entity body of the HTTP request using the application/x-www-form-urlencoded format as defined by [Appendix B](#application-x-www-form-urlencoded). Form Serialization is typically used in HTTP POST requests.[¶](#appendix-C.2-1)

### [C.3.](#appendix-C.3) [JSON Serialization](#name-json-serialization)

The parameters are serialized into a JSON \[[RFC8259](#RFC8259)] object structure by adding each parameter at the highest structure level. Parameter names and string values are represented as JSON strings. Numerical values are represented as JSON numbers. Boolean values are represented as JSON booleans. Omitted parameters and parameters with no value SHOULD be omitted from the object and not represented by a JSON null value, unless otherwise specified. A parameter MAY have a JSON object or a JSON array as its value. The order of parameters does not matter and can vary.[¶](#appendix-C.3-1)

## [Appendix D.](#appendix-D) [Extensions](#name-extensions)

Below is a list of well-established extensions at the time of publication:[¶](#appendix-D-1)

- \[[RFC7009](#RFC7009)]: Token Revocation[¶](#appendix-D-2.1.1)
  
  - The Token Revocation extension defines a mechanism for clients to indicate to the authorization server that an access token is no longer needed.[¶](#appendix-D-2.1.2.1.1)
- \[[RFC7591](#RFC7591)]: Dynamic Client Registration[¶](#appendix-D-2.2.1)
  
  - Dynamic Client Registration provides a mechanism for programmatically registering clients with an authorization server.[¶](#appendix-D-2.2.2.1.1)
- \[[RFC7662](#RFC7662)]: Token Introspection[¶](#appendix-D-2.3.1)
  
  - The Token Introspection extension defines a mechanism for resource servers to obtain information about access tokens.[¶](#appendix-D-2.3.2.1.1)
- \[[RFC8414](#RFC8414)]: Authorization Server Metadata[¶](#appendix-D-2.4.1)
  
  - Authorization Server Metadata (also known as OAuth Discovery) defines an endpoint clients can use to look up the information needed to interact with a particular OAuth server, such as the location of the authorization and token endpoints and the supported grant types.[¶](#appendix-D-2.4.2.1.1)
- \[[RFC8628](#RFC8628)]: OAuth 2.0 Device Authorization Grant[¶](#appendix-D-2.5.1)
  
  - The Device Authorization Grant (formerly known as the Device Flow) is an extension that enables devices with no browser or limited input capability to obtain an access token. This is commonly used by smart TV apps, or devices like hardware video encoders that can stream video to a streaming video service.[¶](#appendix-D-2.5.2.1.1)
- \[[RFC8705](#RFC8705)]: Mutual TLS[¶](#appendix-D-2.6.1)
  
  - Mutual TLS describes a mechanism of binding tokens to the clients they were issued to, as well as a client authentication mechanism, via TLS certificate authentication.[¶](#appendix-D-2.6.2.1.1)
- \[[RFC8707](#RFC8707)]: Resource Indicators[¶](#appendix-D-2.7.1)
  
  - Provides a way for the client to explicitly signal to the authorization server where it intends to use the access token it is requesting.[¶](#appendix-D-2.7.2.1.1)
- \[[RFC9068](#RFC9068)]: JSON Web Token (JWT) Profile for OAuth 2.0 Access Tokens[¶](#appendix-D-2.8.1)
  
  - This specification defines a profile for issuing OAuth access tokens in JSON Web Token (JWT) format.[¶](#appendix-D-2.8.2.1.1)
- \[[RFC9126](#RFC9126)]: Pushed Authorization Requests[¶](#appendix-D-2.9.1)
  
  - The Pushed Authorization Requests extension describes a technique of initiating an OAuth flow from the back channel, providing better security and more flexibility for building complex authorization requests.[¶](#appendix-D-2.9.2.1.1)
- \[[RFC9207](#RFC9207)]: Authorization Server Issuer Identification[¶](#appendix-D-2.10.1)
  
  - The `iss` parameter in the authorization response indicates the identity of the authorization server to prevent mix-up attacks in the client.[¶](#appendix-D-2.10.2.1.1)
- \[[RFC9396](#RFC9396)]: Rich Authorization Requests[¶](#appendix-D-2.11.1)
  
  - Rich Authorization Requests specifies a new parameter `authorization_details` that is used to carry fine-grained authorization data in the OAuth authorization request.[¶](#appendix-D-2.11.2.1.1)
- \[[RFC9449](#RFC9449)]: Demonstrating Proof of Possession (DPoP)[¶](#appendix-D-2.12.1)
  
  - DPoP describes a mechanism for sender-constraining OAuth 2.0 tokens via a proof-of-possession mechanism on the application level.[¶](#appendix-D-2.12.2.1.1)
- \[[RFC9470](#RFC9470)]: Step-Up Authentication Challenge Protocol[¶](#appendix-D-2.13.1)
  
  - Step-Up Auth describes a mechanism that resource servers can use to signal to a client that the authentication event associated with the access token of the current request does not meet its authentication requirements.[¶](#appendix-D-2.13.2.1.1)

## [Appendix E.](#appendix-E) [Acknowledgements](#name-acknowledgements)

This specification is the work of the OAuth Working Group, and its starting point was based on the contents of the following specifications: OAuth 2.0 Authorization Framework (RFC 6749), OAuth 2.0 for Native Apps (RFC 8252), OAuth Security Best Current Practice, and OAuth 2.0 for Browser-Based Apps. The editors would like to thank everyone involved in the creation of those specifications upon which this is built.[¶](#appendix-E-1)

The editors would also like to thank the following individuals for their ideas, feedback, corrections, and wording that helped shape this version of the specification: Andrii Deinega, Bob Hamburg, Brian Campbell, Daniel Fett, Deng Chao, Emelia Smith, Falko, Filip Skokan, Joseph Heenan, Justin Richer, Karsten Meyer zu Selhausen, Michael Jones, Michael Peck, Roberto Polli, Tim Würtele and Vittorio Bertocci.[¶](#appendix-E-2)

Discussions around this specification have also occurred at the OAuth Security Workshop in 2021 and 2022. The authors thank the organizers of the workshop (Guido Schmitz, Steinar Noem, and Daniel Fett) for hosting an event that's conducive to collaboration and community input.[¶](#appendix-E-3)

## [Appendix F.](#appendix-F) [Document History](#name-document-history)

\[\[ To be removed from the final specification ]][¶](#appendix-F-1)

-15[¶](#appendix-F-2)

- add additional context for JWT client authentication and specifically recommend RFC7523bis[¶](#appendix-F-3.1.1)
- editorial clarifications and updates[¶](#appendix-F-3.2.1)
- clarify error responses in authorization endpoint and token endpoint[¶](#appendix-F-3.3.1)
- synced language from RFC9700 for AS open redirect considerations[¶](#appendix-F-3.4.1)
- applied RFC6750 erratas 3500 and 6613[¶](#appendix-F-3.5.1)
- resolved ambiguity around repeated parameters[¶](#appendix-F-3.6.1)

-14[¶](#appendix-F-4)

- Editorial clarifications[¶](#appendix-F-5.1.1)
- Corrected an instance of "relying party" vs "resource server"[¶](#appendix-F-5.2.1)
- Add references to `client_secret_post` and `client_secret_basic` terms from RFC7591[¶](#appendix-F-5.3.1)
- Replaced "sanitize" language with treating as untrusted input[¶](#appendix-F-5.4.1)
- Clarified that native apps guidance applies primarily to mobile app platforms[¶](#appendix-F-5.5.1)
- Clarify that there is no requirement that an AS supports public or confidential clients in particular[¶](#appendix-F-5.6.1)

-13[¶](#appendix-F-6)

- Updated references to RFC 9700[¶](#appendix-F-7.1.1)
- Updated and sorted list of OAuth extensions[¶](#appendix-F-7.2.1)
- Updated references to link to section numbers[¶](#appendix-F-7.3.1)

-12[¶](#appendix-F-8)

- Updated language around client registration to better reflect alternative registration methods such as those in use by OpenID Federation and open ecosystems[¶](#appendix-F-9.1.1)
- Added DPoP and Step-Up Auth to appendix of extensions[¶](#appendix-F-9.2.1)
- Updated reference for case insensitivity of auth scheme to HTTP instead of ABNF[¶](#appendix-F-9.3.1)
- Corrected an instance of "relying party" vs "client"[¶](#appendix-F-9.4.1)
- Moved `client_id` requirement to the individual grant types[¶](#appendix-F-9.5.1)
- Consolidated the descriptions of serialization methods to the appendix[¶](#appendix-F-9.6.1)

-11[¶](#appendix-F-10)

- Explicitly mention that Bearer is case insensitive[¶](#appendix-F-11.1.1)
- Recommend against defining custom scopes that conflict with known scopes[¶](#appendix-F-11.2.1)
- Change client credentials to be required to be supported in the request body to avoid HTTP Basic authentication encoding interop issues[¶](#appendix-F-11.3.1)

-10[¶](#appendix-F-12)

- Clarify that the client id is an opaque string[¶](#appendix-F-13.1.1)
- Extensions may define additional error codes on a resource request[¶](#appendix-F-13.2.1)
- Improved formatting for error field definitions[¶](#appendix-F-13.3.1)
- Moved and expanded "scope" definition to introduction section[¶](#appendix-F-13.4.1)
- Split access token section into structure and request[¶](#appendix-F-13.5.1)
- Renamed b64token to token68 for consistency with RFC7235[¶](#appendix-F-13.6.1)
- Restored content from old appendix B about application/x-www-form-urlencoded[¶](#appendix-F-13.7.1)
- Clarified that clients must not parse access tokens[¶](#appendix-F-13.8.1)
- Expanded text around when `redirect_uri` parameter is required in the authorization request[¶](#appendix-F-13.9.1)
- Changed "permissions" to "privileges" in refresh token section for consistency[¶](#appendix-F-13.10.1)
- Consolidated authorization code flow security considerations[¶](#appendix-F-13.11.1)
- Clarified authorization code reuse - an authorization code can only obtain an access token once[¶](#appendix-F-13.12.1)

-09[¶](#appendix-F-14)

- AS MUST NOT support CORS requests at authorization endpoint[¶](#appendix-F-15.1.1)
- more detail on asymmetric client authentication[¶](#appendix-F-15.2.1)
- sync CSRF description from security BCP[¶](#appendix-F-15.3.1)
- update and move sender-constrained access tokens section[¶](#appendix-F-15.4.1)
- sync client impersonating resource owner with security BCP[¶](#appendix-F-15.5.1)
- add reference to authorization request from redirect URI registration section[¶](#appendix-F-15.6.1)
- sync refresh rotation section from security BCP[¶](#appendix-F-15.7.1)
- sync redirect URI matching text from security BCP[¶](#appendix-F-15.8.1)
- updated references to RAR (RFC9396)[¶](#appendix-F-15.9.1)
- clarifications on URIs[¶](#appendix-F-15.10.1)
- removed redirect\_uri from the token request[¶](#appendix-F-15.11.1)
- expanded security considerations around code\_verifier[¶](#appendix-F-15.12.1)
- revised introduction section[¶](#appendix-F-15.13.1)

-08[¶](#appendix-F-16)

- Updated acknowledgments[¶](#appendix-F-17.1.1)
- Swap "by a trusted party" with "by an outside party" in client ID definition[¶](#appendix-F-17.2.1)
- Replaced "verify the identity of the resource owner" with "authenticate"[¶](#appendix-F-17.3.1)
- Clarified refresh token rotation to match RFC6819[¶](#appendix-F-17.4.1)
- Added appendix to hold application/x-www-form-urlencoded examples[¶](#appendix-F-17.5.1)
- Fixed references to entries in appendix[¶](#appendix-F-17.6.1)
- Incorporated new "Phishing via AS" section from Security BCP[¶](#appendix-F-17.7.1)
- Rephrase description of the motivation for client authentication[¶](#appendix-F-17.8.1)
- Moved "scope" parameter in token request into specific grant types to match OAuth 2.0[¶](#appendix-F-17.9.1)
- Updated Clickjacking and Open Redirection description from the latest version of the Security BCP[¶](#appendix-F-17.10.1)
- Moved normative requirements out of authorization code security considerations section[¶](#appendix-F-17.11.1)
- Security considerations clarifications, and removed a duplicate section[¶](#appendix-F-17.12.1)

-07[¶](#appendix-F-18)

- Removed "third party" from abstract[¶](#appendix-F-19.1.1)
- Added MFA and passwordless as additional motiviations in introduction[¶](#appendix-F-19.2.1)
- Mention PAR as one way redirect URI registration can happen[¶](#appendix-F-19.3.1)
- Added a reference to requiring CORS headers on the token endpoint[¶](#appendix-F-19.4.1)
- Updated reference to OMAP extension[¶](#appendix-F-19.5.1)
- Fixed numbering in sequence diagram[¶](#appendix-F-19.6.1)

-06[¶](#appendix-F-20)

- Removed "credentialed client" term[¶](#appendix-F-21.1.1)
- Simplified definition of "confidential" and "public" clients[¶](#appendix-F-21.2.1)
- Incorporated the `iss` response parameter referencing RFC9207[¶](#appendix-F-21.3.1)
- Added section on access token validation by the RS[¶](#appendix-F-21.4.1)
- Removed requirement for authorization servers to support all 3 redirect methods for native apps[¶](#appendix-F-21.5.1)
- Fixes for some references[¶](#appendix-F-21.6.1)
- Updates HTTP references to RFC 9110[¶](#appendix-F-21.7.1)
- Clarifies "authorization grant" term[¶](#appendix-F-21.8.1)
- Clarifies client credential grant usage[¶](#appendix-F-21.9.1)
- Clean up authorization code diagram[¶](#appendix-F-21.10.1)
- Updated reference for application/x-www-form-urlencoded and removed outdated note about it not being in the IANA registry[¶](#appendix-F-21.11.1)

-05[¶](#appendix-F-22)

- Added a section about the removal of the implicit flow[¶](#appendix-F-23.1.1)
- Moved many normative requirements from security considerations into the appropriate inline sections[¶](#appendix-F-23.2.1)
- Reorganized and consolidated TLS language[¶](#appendix-F-23.3.1)
- Require TLS on redirect URIs except for localhost/custom URL scheme[¶](#appendix-F-23.4.1)
- Updated refresh token guidance to match security BCP[¶](#appendix-F-23.5.1)

-04[¶](#appendix-F-24)

- Added explicit mention of not sending access tokens in URI query strings[¶](#appendix-F-25.1.1)
- Clarifications on definition of client types[¶](#appendix-F-25.2.1)
- Consolidated text around loopback vs localhost[¶](#appendix-F-25.3.1)
- Editorial clarifications throughout the document[¶](#appendix-F-25.4.1)

-03[¶](#appendix-F-26)

- refactoring to collect all the grant types under the same top-level header in section 4[¶](#appendix-F-27.1.1)
- Better split normative and security consideration text into the appropriate places, both moving text that was really security considerations out of the main part of the document, as well as pulling normative requirements from the security considerations sections into the appropriate part of the main document[¶](#appendix-F-27.2.1)
- Incorporated many of the published errata on RFC6749[¶](#appendix-F-27.3.1)
- Updated references to various RFCs[¶](#appendix-F-27.4.1)
- Editorial clarifications throughout the document[¶](#appendix-F-27.5.1)

-02[¶](#appendix-F-28)

-01[¶](#appendix-F-29)

-00[¶](#appendix-F-30)

- initial revision[¶](#appendix-F-31.1.1)

## [Authors' Addresses](#name-authors-addresses)

Dick Hardt

Hellō

Aaron Parecki

Okta

Torsten Lodderstedt

SPRIND