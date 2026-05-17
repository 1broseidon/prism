RFC 9728 OAuth 2.0 Protected Resource Metadata April 2025 Jones, et al. Standards Track \[Page]

## RFC 9728

## [Abstract](#abstract)

This specification defines a metadata format that an OAuth 2.0 client or authorization server can use to obtain the information needed to interact with an OAuth 2.0 protected resource.[¶](#section-abstract-1)

## [Status of This Memo](#name-status-of-this-memo)

This is an Internet Standards Track document.[¶](#section-boilerplate.1-1)

This document is a product of the Internet Engineering Task Force (IETF). It represents the consensus of the IETF community. It has received public review and has been approved for publication by the Internet Engineering Steering Group (IESG). Further information on Internet Standards is available in Section 2 of RFC 7841.[¶](#section-boilerplate.1-2)

Information about the current status of this document, any errata, and how to provide feedback on it may be obtained at [https://www.rfc-editor.org/info/rfc9728](https://www.rfc-editor.org/info/rfc9728).[¶](#section-boilerplate.1-3)

## [Copyright Notice](#name-copyright-notice)

Copyright (c) 2025 IETF Trust and the persons identified as the document authors. All rights reserved.[¶](#section-boilerplate.2-1)

This document is subject to BCP 78 and the IETF Trust's Legal Provisions Relating to IETF Documents ([https://trustee.ietf.org/license-info](https://trustee.ietf.org/license-info)) in effect on the date of publication of this document. Please review these documents carefully, as they describe your rights and restrictions with respect to this document. Code Components extracted from this document must include Revised BSD License text as described in Section 4.e of the Trust Legal Provisions and are provided without warranty as described in the Revised BSD License.[¶](#section-boilerplate.2-2)

[▲](#)

## [Table of Contents](#name-table-of-contents)

- [1](#section-1).  [Introduction](#name-introduction)
  
  - [1.1](#section-1.1).  [Requirements Notation and Conventions](#name-requirements-notation-and-c)
  - [1.2](#section-1.2).  [Terminology](#name-terminology)
- [2](#section-2).  [Protected Resource Metadata](#name-protected-resource-metadata)
  
  - [2.1](#section-2.1).  [Human-Readable Resource Metadata](#name-human-readable-resource-met)
  - [2.2](#section-2.2).  [Signed Protected Resource Metadata](#name-signed-protected-resource-m)
- [3](#section-3).  [Obtaining Protected Resource Metadata](#name-obtaining-protected-resourc)
  
  - [3.1](#section-3.1).  [Protected Resource Metadata Request](#name-protected-resource-metadata-)
  - [3.2](#section-3.2).  [Protected Resource Metadata Response](#name-protected-resource-metadata-r)
  - [3.3](#section-3.3).  [Protected Resource Metadata Validation](#name-protected-resource-metadata-v)
- [4](#section-4).  [Authorization Server Metadata](#name-authorization-server-metada)
- [5](#section-5).  [Use of WWW-Authenticate for Protected Resource Metadata](#name-use-of-www-authenticate-for)
  
  - [5.1](#section-5.1).  [WWW-Authenticate Response](#name-www-authenticate-response)
  - [5.2](#section-5.2).  [Changes to Resource Metadata](#name-changes-to-resource-metadat)
  - [5.3](#section-5.3).  [Client Identifier and Client Authentication](#name-client-identifier-and-clien)
  - [5.4](#section-5.4).  [Compatibility with Other Authentication Methods](#name-compatibility-with-other-au)
- [6](#section-6).  [String Operations](#name-string-operations)
- [7](#section-7).  [Security Considerations](#name-security-considerations)
  
  - [7.1](#section-7.1).  [TLS Requirements](#name-tls-requirements)
  - [7.2](#section-7.2).  [Scopes](#name-scopes)
  - [7.3](#section-7.3).  [Impersonation Attacks](#name-impersonation-attacks)
  - [7.4](#section-7.4).  [Audience-Restricted Access Tokens](#name-audience-restricted-access-)
  - [7.5](#section-7.5).  [Publishing Metadata in a Standard Format](#name-publishing-metadata-in-a-st)
  - [7.6](#section-7.6).  [Authorization Servers](#name-authorization-servers)
  - [7.7](#section-7.7).  [Server-Side Request Forgery (SSRF)](#name-server-side-request-forgery)
  - [7.8](#section-7.8).  [Phishing](#name-phishing)
  - [7.9](#section-7.9).  [Differences Between Unsigned and Signed Metadata](#name-differences-between-unsigne)
  - [7.10](#section-7.10). [Metadata Caching](#name-metadata-caching)
- [8](#section-8).  [IANA Considerations](#name-iana-considerations)
  
  - [8.1](#section-8.1).  [OAuth Protected Resource Metadata Registry](#name-oauth-protected-resource-me)
    
    - [8.1.1](#section-8.1.1).  [Registration Template](#name-registration-template)
    - [8.1.2](#section-8.1.2).  [Initial Registry Contents](#name-initial-registry-contents)
  - [8.2](#section-8.2).  [OAuth Authorization Server Metadata Registry](#name-oauth-authorization-server-)
    
    - [8.2.1](#section-8.2.1).  [Registry Contents](#name-registry-contents)
  - [8.3](#section-8.3).  [Well-Known URIs Registry](#name-well-known-uris-registry)
    
    - [8.3.1](#section-8.3.1).  [Registry Contents](#name-registry-contents-2)
- [9](#section-9).  [References](#name-references)
  
  - [9.1](#section-9.1).  [Normative References](#name-normative-references)
  - [9.2](#section-9.2).  [Informative References](#name-informative-references)
- [](#appendix-A)[Acknowledgements](#name-acknowledgements)
- [](#appendix-B)[Authors' Addresses](#name-authors-addresses)

## [1.](#section-1) [Introduction](#name-introduction)

This specification defines a metadata format enabling OAuth 2.0 clients and authorization servers to obtain information needed to interact with an OAuth 2.0 protected resource. The structure and content of this specification are intentionally as parallel as possible to (1) ["OAuth 2.0 Dynamic Client Registration Protocol"](#RFC7591) \[[RFC7591](#RFC7591)], which enables a client to provide metadata about itself to an OAuth 2.0 authorization server and (2) "[OAuth 2.0 Authorization Server Metadata](#RFC8414)" \[[RFC8414](#RFC8414)], which enables a client to obtain metadata about an OAuth 2.0 authorization server.[¶](#section-1-1)

The means by which the client obtains the location of the protected resource is out of scope for this document. In some cases, the location may be manually configured into the client; for example, an email client could provide an interface for a user to enter the URL of their [JSON Meta Application Protocol (JMAP) server](#RFC8620) \[[RFC8620](#RFC8620)]. In other cases, it may be dynamically discovered; for example, a user could enter their email address into an email client, the client could perform [WebFinger discovery](#RFC7033) \[[RFC7033](#RFC7033)] (in a manner related to the description in [Section 2](https://openid.net/specs/openid-connect-discovery-1_0.html#IssuerDiscovery) of \[[OpenID.Discovery](#OpenID.Discovery)]) to find the resource server, and the client could then fetch the resource server metadata to find the authorization server to use to obtain authorization to access the user's email.[¶](#section-1-2)

The metadata for a protected resource is retrieved from a well-known location as a JSON \[[RFC8259](#RFC8259)] document, which declares information about its capabilities and, optionally, its relationships with other services. This process is described in [Section 3](#PRConfig).[¶](#section-1-3)

This metadata can be communicated either in a self-asserted fashion or as a set of signed metadata values represented as claims in a JSON Web Token (JWT) \[[JWT](#RFC7519)]. In the JWT case, the issuer is vouching for the validity of the data about the protected resource. This is analogous to the role that the software statement plays in OAuth Dynamic Client Registration \[[RFC7591](#RFC7591)].[¶](#section-1-4)

Each protected resource publishing metadata about itself makes its own metadata document available at a well-known location deterministically derived from the protected resource's URL, even when the resource server implements multiple protected resources. This prevents attackers from publishing metadata that supposedly describes the protected resource but that is not actually authoritative for the protected resource, as described in [Section 7.3](#Impersonation).[¶](#section-1-5)

[Section 2](#PRMetadata) defines metadata parameters that a protected resource can publish, which includes things like which scopes are supported, how a client can present an access token, and more. These values, such as the `jwks_uri` (see [Section 2](#PRMetadata)), may be used with other specifications; for example, the public keys published in the `jwks_uri` can be used to verify the signed resource responses, as described in \[[FAPI.MessageSigning](#FAPI.MessageSigning)].[¶](#section-1-6)

[Section 5](#WWW-Authenticate) describes the use of `WWW-Authenticate` by protected resources to dynamically inform clients of the URL of their protected resource metadata. This use of `WWW-Authenticate` can indicate that the protected resource metadata may have changed.[¶](#section-1-7)

### [1.1.](#section-1.1) [Requirements Notation and Conventions](#name-requirements-notation-and-c)

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in BCP 14 \[[RFC2119](#RFC2119)] \[[RFC8174](#RFC8174)] when, and only when, they appear in all capitals, as shown here.[¶](#section-1.1-1)

All applications of [JSON Web Signature (JWS) data structures](#RFC7515) \[[JWS](#RFC7515)] and [JSON Web Encryption (JWE) data structures](#RFC7516) \[[JWE](#RFC7516)] as discussed in this specification utilize the JWS Compact Serialization or the JWE Compact Serialization; the JWS JSON Serialization and the JWE JSON Serialization are not used. Choosing a single serialization is intended to facilitate interoperability.[¶](#section-1.1-2)

### [1.2.](#section-1.2) [Terminology](#name-terminology)

This specification uses the terms "access token", "authorization code", "authorization server", "client", "client authentication", "client identifier", "protected resource", and "resource server" defined by [OAuth 2.0](#RFC6749) \[[RFC6749](#RFC6749)], and the terms "Claim Name" and "JSON Web Token (JWT)" defined by "[JSON Web Token (JWT)](#RFC7519)" \[[JWT](#RFC7519)].[¶](#section-1.2-1)

This specification defines the following term:[¶](#section-1.2-2)

Resource Identifier:

The protected resource's resource identifier, which is a URL that uses the `https` scheme and has no fragment component. As specified in [Section 2](https://rfc-editor.org/rfc/rfc8707#section-2) of \[[RFC8707](#RFC8707)], it also SHOULD NOT include a query component, but it is recognized that there are cases that make a query component a useful and necessary part of a resource identifier. Protected resource metadata is published at a `.well-known` location \[[RFC8615](#RFC8615)] derived from this resource identifier, as described in [Section 3](#PRConfig).[¶](#section-1.2-3.2)

## [3.](#section-3) [Obtaining Protected Resource Metadata](#name-obtaining-protected-resourc)

Protected resources supporting metadata MUST make a JSON document containing metadata as specified in [Section 2](#PRMetadata) available at a URL formed by inserting a well-known URI string into the protected resource's resource identifier between the host component and the path and/or query components, if any. By default, the well-known URI string used is `/.well-known/oauth-protected-resource`. The syntax and semantics of `.well-known` are defined in \[[RFC8615](#RFC8615)]. The well-known URI path suffix used MUST be registered in the "Well-Known URIs" registry \[[IANA.well-known](#IANA.well-known)]. Examples of this construction can be found in [Section 3.1](#PRConfigurationRequest).[¶](#section-3-1)

The term "application", as used below (and as used in \[[RFC8414](#RFC8414)]), encompasses all the components used to accomplish the task for the use case. That can include OAuth clients, authorization servers, protected resources, and non-OAuth components, inclusive of the code running in each of them. Applications are built to solve particular problems and may utilize many components and services.[¶](#section-3-2)

Different applications utilizing OAuth protected resources in application-specific ways MAY define and register different well-known URI path suffixes for publishing protected resource metadata used by those applications. For instance, if the Example application uses an OAuth protected resource in an Example-specific way and there are Example-specific metadata values that it needs to publish, then it might register and use the `example-protected-resource` URI path suffix and publish the metadata document at the URL formed by inserting `/.well-known/example-protected-resource` between the host and path and/or query components of the protected resource's resource identifier. Alternatively, many such applications will use the default well-known URI string `/.well-known/oauth-protected-resource`, which is the right choice for general-purpose OAuth protected resources, and not register an application-specific one.[¶](#section-3-3)

An OAuth 2.0 application using this specification MUST specify what well-known URI suffix it will use for this purpose. The same protected resource MAY choose to publish its metadata at multiple well-known locations derived from its resource identifier -- for example, publishing metadata at both `/.well-known/example-protected-resource` and `/.well-known/oauth-protected-resource`.[¶](#section-3-4)

### [3.1.](#section-3.1) [Protected Resource Metadata Request](#name-protected-resource-metadata-)

A protected resource metadata document MUST be queried using an HTTP `GET` request at the previously specified URL.[¶](#section-3.1-1)

The consumer of the metadata would make the following request when the resource identifier is `https://resource.example.com` and the well-known URI path suffix is `oauth-protected-resource` to obtain the metadata, since the resource identifier contains no path component:[¶](#section-3.1-2)

```
  GET /.well-known/oauth-protected-resource HTTP/1.1
  Host: resource.example.com
```

[¶](#section-3.1-3)

If the resource identifier value contains a path or query component, any terminating slash (`/`) following the host component MUST be removed before inserting `/.well-known/` and the well-known URI path suffix between the host component and the path and/or query components. The consumer of the metadata would make the following request when the resource identifier is `https://resource.example.com/resource1` and the well-known URI path suffix is `oauth-protected-resource` to obtain the metadata, since the resource identifier contains a path component:[¶](#section-3.1-4)

```
  GET /.well-known/oauth-protected-resource/resource1 HTTP/1.1
  Host: resource.example.com
```

[¶](#section-3.1-5)

Using path components enables supporting multiple resources per host. This is required in some multi-tenant hosting configurations. This use of `.well-known` is for supporting multiple resources per host; unlike its use in \[[RFC8615](#RFC8615)], it does not provide general information about the host.[¶](#section-3.1-6)

### [3.2.](#section-3.2) [Protected Resource Metadata Response](#name-protected-resource-metadata-r)

The response is a set of metadata parameters about the protected resource's configuration. A successful response MUST use the 200 OK HTTP status code and return a JSON object using the `application/json` content type that contains a set of metadata parameters as its members that are a subset of the metadata parameters defined in [Section 2](#PRMetadata). Additional metadata parameters MAY be defined and used; any metadata parameters that are not understood MUST be ignored.[¶](#section-3.2-1)

Parameters with multiple values are represented as JSON arrays. Parameters with zero values MUST be omitted from the response.[¶](#section-3.2-2)

An error response uses the applicable HTTP status code value.[¶](#section-3.2-3)

The following is a non-normative example response:[¶](#section-3.2-4)

```
  HTTP/1.1 200 OK
  Content-Type: application/json

  {
   "resource":
     "https://resource.example.com",
   "authorization_servers":
     ["https://as1.example.com",
      "https://as2.example.net"],
   "bearer_methods_supported":
     ["header", "body"],
   "scopes_supported":
     ["profile", "email", "phone"],
   "resource_documentation":
     "https://resource.example.com/resource_documentation.html"
  }
```

[¶](#section-3.2-5)

### [3.3.](#section-3.3) [Protected Resource Metadata Validation](#name-protected-resource-metadata-v)

The `resource` value returned MUST be identical to the protected resource's resource identifier value into which the well-known URI path suffix was inserted to create the URL used to retrieve the metadata. If these values are not identical, the data contained in the response MUST NOT be used.[¶](#section-3.3-1)

If the protected resource metadata was retrieved from a URL returned by the protected resource via the `WWW-Authenticate` `resource_metadata` parameter, then the `resource` value returned MUST be identical to the URL that the client used to make the request to the resource server. If these values are not identical, the data contained in the response MUST NOT be used.[¶](#section-3.3-2)

These validation actions can thwart impersonation attacks, as described in [Section 7.3](#Impersonation).[¶](#section-3.3-3)

The recipient MUST validate that any signed metadata was signed by a key belonging to the issuer and that the signature is valid. If the signature does not validate or the issuer is not trusted, the recipient SHOULD treat this as an error condition.[¶](#section-3.3-4)

## [5.](#section-5) [Use of WWW-Authenticate for Protected Resource Metadata](#name-use-of-www-authenticate-for)

A protected resource MAY use the `WWW-Authenticate` HTTP response header field, as discussed in \[[RFC9110](#RFC9110)], to return a URL to its protected resource metadata to the client. The client can then retrieve protected resource metadata as described in [Section 3](#PRConfig). The client might then, for instance, determine what authorization server to use for the resource based on protected resource metadata retrieved.[¶](#section-5-1)

A typical end-to-end flow doing so is as follows. Note that while this example uses the OAuth 2.0 authorization code flow, a similar sequence could also be implemented with any other OAuth flow.[¶](#section-5-2)

Client Client Resource Server Resource Server Authorization Server Authorization Server 1. Resource Request Without Access Token 2. WWW-Authenticate 3. Fetch RS Metadata 4. RS Metadata Response 5. Validate RS Metadata, Build AS Metadata URL 6. Fetch AS Metadata 7. AS Metadata Response 8-9. OAuth Authorization Flow Client Obtains Access Token 10. Resource Request With Access Token 11. Resource Response

[¶](#section-5-3.1.1)

[Figure 1](#figure-1): [Sequence Diagram](#name-sequence-diagram)

01. The client makes a request to a protected resource without presenting an access token.[¶](#section-5-4.1.1)
02. The resource server responds with a `WWW-Authenticate` header including the URL of the protected resource metadata.[¶](#section-5-4.2.1)
03. The client fetches the protected resource metadata from this URL.[¶](#section-5-4.3.1)
04. The resource server responds with the protected resource metadata according to [Section 3.2](#PRConfigurationResponse).[¶](#section-5-4.4.1)
05. The client validates the protected resource metadata, as described in [Section 3.3](#PRConfigurationValidation), and builds the authorization server metadata URL from an issuer identifier in the resource metadata according to \[[RFC8414](#RFC8414)].[¶](#section-5-4.5.1)
06. The client makes a request to fetch the authorization server metadata.[¶](#section-5-4.6.1)
07. The authorization server responds with the authorization server metadata document according to \[[RFC8414](#RFC8414)].[¶](#section-5-4.7.1)
08. The client directs the user agent to the authorization server to begin the authorization flow.[¶](#section-5-4.8.1)
09. The authorization exchange is completed and the authorization server returns an access token to the client.[¶](#section-5-4.9.1)
10. The client repeats the resource request from step 1, presenting the newly obtained access token.[¶](#section-5-4.10.1)
11. The resource server returns the requested protected resource.[¶](#section-5-4.11.1)

### [5.1.](#section-5.1) [WWW-Authenticate Response](#name-www-authenticate-response)

This specification introduces a new parameter in the `WWW-Authenticate` HTTP response header field to indicate the protected resource metadata URL:[¶](#section-5.1-1)

resource\_metadata:

The URL of the protected resource metadata.[¶](#section-5.1-2.2)

The response below is an example of a `WWW-Authenticate` header that includes the resource identifier.[¶](#section-5.1-3)

```
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer resource_metadata=
  "https://resource.example.com/.well-known/oauth-protected-resource"
```

[¶](#section-5.1-4)

The HTTP status code in the example response above is defined by \[[RFC6750](#RFC6750)].[¶](#section-5.1-5)

This parameter MAY also be used in `WWW-Authenticate` responses using `authorization` schemes other than `"Bearer"` \[[RFC6750](#RFC6750)], such as the `DPoP` scheme defined by \[[RFC9449](#RFC9449)].[¶](#section-5.1-6)

The `resource_metadata` parameter MAY be combined with other parameters defined in other extensions, such as the `max_age` parameter defined by \[[RFC9470](#RFC9470)].[¶](#section-5.1-7)

### [5.2.](#section-5.2) [Changes to Resource Metadata](#name-changes-to-resource-metadat)

At any point, for any reason determined by the resource server, the protected resource MAY respond with a new `WWW-Authenticate` challenge that includes a value for the protected resource metadata URL to indicate that its metadata may have changed. If the client receives such a `WWW-Authenticate` response, it SHOULD retrieve the updated protected resource metadata and use the new metadata values obtained, after validating them as described in [Section 3.3](#PRConfigurationValidation). Among other things, this enables a resource server to change which authorization servers it uses without any other coordination with clients.[¶](#section-5.2-1)

### [5.3.](#section-5.3) [Client Identifier and Client Authentication](#name-client-identifier-and-clien)

The way in which the client identifier is established at the authorization server is out of scope for this specification.[¶](#section-5.3-1)

This specification is intended to be deployed in scenarios where the client has no prior knowledge about the resource server and where the resource server might or might not have prior knowledge about the client.[¶](#section-5.3-2)

There are some existing methods by which an unrecognized client can make use of an authorization server, such as using Dynamic Client Registration \[[RFC7591](#RFC7591)] to register the client prior to initiating the authorization flow. Future OAuth extensions might define alternatives, such as using URLs to identify clients.[¶](#section-5.3-3)

### [5.4.](#section-5.4) [Compatibility with Other Authentication Methods](#name-compatibility-with-other-au)

Resource servers MAY return other `WWW-Authenticate` headers indicating various authentication schemes. This allows the resource server to support clients that may or may not implement this specification and allows clients to choose their preferred authentication scheme.[¶](#section-5.4-1)

## [6.](#section-6) [String Operations](#name-string-operations)

Processing some OAuth 2.0 messages requires comparing values in the messages to known values. For example, the member names in the metadata response might be compared to specific member names such as `resource`. Comparing Unicode strings \[[UNICODE](#UNICODE)], however, has significant security implications.[¶](#section-6-1)

Therefore, comparisons between JSON strings and other Unicode strings MUST be performed as specified below:[¶](#section-6-2)

1. Remove any JSON-applied escaping to produce an array of Unicode code points.[¶](#section-6-3.1.1)
2. Unicode Normalization \[[USA15](#USA15)] MUST NOT be applied at any point to either the JSON string or the string it is to be compared against.[¶](#section-6-3.2.1)
3. Comparisons between the two strings MUST be performed as a Unicode code-point-to-code-point equality comparison.[¶](#section-6-3.3.1)

Note that this is the same equality comparison procedure as that described in [Section 8.3](https://rfc-editor.org/rfc/rfc8259#section-8.3) of \[[RFC8259](#RFC8259)].[¶](#section-6-4)

## [7.](#section-7) [Security Considerations](#name-security-considerations)

### [7.1.](#section-7.1) [TLS Requirements](#name-tls-requirements)

Implementations MUST support TLS. They MUST follow the guidance in \[[BCP195](#BCP195)], which provides recommendations and requirements for improving the security of deployed services that use TLS.[¶](#section-7.1-1)

The use of TLS at the protected resource metadata URLs protects against information disclosure and tampering.[¶](#section-7.1-2)

### [7.2.](#section-7.2) [Scopes](#name-scopes)

The `scopes_supported` parameter is the list of scopes the resource server is willing to disclose that it supports. It is not meant to indicate that an OAuth client should request all scopes in the list. The client SHOULD still follow OAuth best practices and request tokens with as limited a scope as possible for the given operation, as described in [Section 2.3](https://rfc-editor.org/rfc/rfc9700#section-2.3) of "Best Current Practice for OAuth 2.0 Security" \[[RFC9700](#RFC9700)].[¶](#section-7.2-1)

### [7.3.](#section-7.3) [Impersonation Attacks](#name-impersonation-attacks)

TLS certificate checking MUST be performed by the client as described in \[[RFC9525](#RFC9525)] when making a protected resource metadata request. Checking that the server certificate is valid for the resource identifier URL prevents adversary-in-the-middle and DNS-based attacks. These attacks could cause a client to be tricked into using an attacker's resource server, which would enable impersonation of the legitimate protected resource. If an attacker can accomplish this, they can access the resources that the affected client has access to, using the protected resource that they are impersonating.[¶](#section-7.3-1)

An attacker may also attempt to impersonate a protected resource by publishing a metadata document that contains a `resource` metadata parameter using the resource identifier URL of the protected resource being impersonated but that contains information of the attacker's choosing. This would enable it to impersonate that protected resource, if accepted by the client. To prevent this, the client MUST ensure that the resource identifier URL it is using as the prefix for the metadata request exactly matches the value of the `resource` metadata parameter in the protected resource metadata document received by the client, as described in [Section 3.3](#PRConfigurationValidation).[¶](#section-7.3-2)

### [7.4.](#section-7.4) [Audience-Restricted Access Tokens](#name-audience-restricted-access-)

If a client expects to interact with multiple resource servers, the client SHOULD request audience-restricted access tokens using \[[RFC8707](#RFC8707)], and the authorization server SHOULD support audience-restricted access tokens.[¶](#section-7.4-1)

Without audience-restricted access tokens, a malicious resource server (RS1) may be able to use the `WWW-Authenticate` header to get a client to request an access token with a scope used by a legitimate resource server (RS2), and after the client sends a request to RS1, then RS1 could reuse the access token at RS2.[¶](#section-7.4-2)

While this attack is not explicitly enabled by this specification and is possible in a plain OAuth 2.0 deployment, it is made somewhat more likely by the use of dynamically configured clients. As such, the use of audience-restricted access tokens and Resource Indicators \[[RFC8707](#RFC8707)] is RECOMMENDED when using the features in this specification.[¶](#section-7.4-3)

### [7.5.](#section-7.5) [Publishing Metadata in a Standard Format](#name-publishing-metadata-in-a-st)

Publishing information about the protected resource in a standard format makes it easier for both legitimate clients and attackers to use the protected resource. Whether a protected resource publishes its metadata in an ad hoc manner or in the standard format defined by this specification, the same defenses against attacks that might be mounted that use this information should be applied.[¶](#section-7.5-1)

### [7.6.](#section-7.6) [Authorization Servers](#name-authorization-servers)

To support use cases in which the set of legitimate authorization servers to use with the protected resource is enumerable, this specification defines the `authorization_servers` metadata parameter, which enables explicitly listing them. Note that if the set of legitimate protected resources to use with an authorization server is also enumerable, lists in the protected resource metadata and authorization server metadata should be cross-checked against one another for consistency when these lists are used by the application profile.[¶](#section-7.6-1)

Secure determination of appropriate authorization servers to use with a protected resource for all use cases is out of scope for this specification. This specification assumes that the client has a means of determining appropriate authorization servers to use with a protected resource and that the client is using the correct metadata for each protected resource. Implementers need to be aware that if an inappropriate authorization server is used by the client, an attacker may be able to act as an adversary-in-the-middle proxy to a valid authorization server without it being detected by the authorization server or the client.[¶](#section-7.6-2)

The ways to determine the appropriate authorization servers to use with a protected resource are, in general, application dependent. For instance, some protected resources are used with a fixed authorization server or a set of authorization servers, the locations of which may be known via out-of-band mechanisms. Alternatively, as described in this specification, the locations of the authorization servers could be published by the protected resource as metadata values. In other cases, the set of authorization servers that can be used with a protected resource can be dynamically changed by administrative actions or by changes to the set of authorization servers adhering to a trust framework. Many other means of determining appropriate associations between protected resources and authorization servers are also possible.[¶](#section-7.6-3)

### [7.7.](#section-7.7) [Server-Side Request Forgery (SSRF)](#name-server-side-request-forgery)

The OAuth client is expected to fetch the authorization server metadata based on the value of the issuer in the resource server metadata. Since this specification enables clients to interoperate with RSs and ASes it has no prior knowledge of, this opens a risk for Server-Side Request Forgery (SSRF) attacks by malicious users or malicious resource servers. Clients SHOULD take appropriate precautions against SSRF attacks, such as blocking requests to internal IP address ranges. Further recommendations can be found in the Open Worldwide Application Security Project (OWASP) SSRF Prevention Cheat Sheet \[[OWASP.SSRF](#OWASP.SSRF)].[¶](#section-7.7-1)

### [7.8.](#section-7.8) [Phishing](#name-phishing)

This specification may be deployed in a scenario where the desired HTTP resource is identified by a user-selected URL. If this resource is malicious or compromised, it could mislead the user into revealing their account credentials or authorizing unwanted access to OAuth-controlled capabilities. This risk is reduced, but not eliminated, by following best practices for OAuth user interfaces, such as providing clear notice to the user, displaying the authorization server's domain name, supporting origin-bound phishing-resistant authenticators, supporting the use of password managers, and applying heuristic checks such as domain reputation.[¶](#section-7.8-1)

### [7.10.](#section-7.10) [Metadata Caching](#name-metadata-caching)

Protected resource metadata is retrieved using an HTTP `GET` request, as specified in [Section 3.1](#PRConfigurationRequest). Normal HTTP caching behaviors apply, meaning that the `GET` request may retrieve a cached copy of the content, rather than the latest copy. Implementations should utilize HTTP caching directives such as `Cache-Control` with `max-age`, as defined in \[[RFC9111](#RFC9111)], to enable caching of retrieved metadata for appropriate time periods.[¶](#section-7.10-1)

## [8.](#section-8) [IANA Considerations](#name-iana-considerations)

Values are registered via Specification Required \[[RFC8126](#RFC8126)]. Registration requests should be sent to &lt;oauth-ext-review@ietf.org&gt; to initiate a two-week review period. However, to allow for the allocation of values prior to publication of the final version of a specification, the designated experts may approve registration once they are satisfied that the specification will be completed and published. However, if the specification is not completed and published in a timely manner, as determined by the designated experts, the designated experts may request that IANA withdraw the registration.[¶](#section-8-1)

Registration requests sent to the mailing list for review should use an appropriate subject (e.g., "Request to register OAuth Protected Resource Metadata: example").[¶](#section-8-2)

Within the review period, the designated experts will either approve or deny the registration request, communicating this decision to the review list and IANA. Denials should include an explanation and, if applicable, suggestions as to how to make the request successful. If the designated experts are not responsive, the registration requesters should contact IANA to escalate the process.[¶](#section-8-3)

Designated experts should apply the following criteria when reviewing proposed registrations: They must be unique -- that is, they should not duplicate existing functionality; they are likely generally applicable, as opposed to being used for a single application; and they are clear and fit the purpose of the registry.[¶](#section-8-4)

IANA must only accept registry updates from the designated experts and should direct all requests for registration to the review mailing list.[¶](#section-8-5)

In order to enable broadly informed review of registration decisions, there should be multiple designated experts to represent the perspectives of different applications using this specification. In cases where registration may be perceived as a conflict of interest for a particular expert, that expert should defer to the judgment of the other experts.[¶](#section-8-6)

The mailing list is used to enable public review of registration requests, which enables both designated experts and other interested parties to provide feedback on proposed registrations. Designated experts may allocate values prior to publication of the final specification. This allows authors to receive guidance from the designated experts early, so any identified issues can be fixed before the final specification is published.[¶](#section-8-7)

### [8.3.](#section-8.3) [Well-Known URIs Registry](#name-well-known-uris-registry)

This specification registers the well-known URI defined in [Section 3](#PRConfig) in the "Well-Known URIs" registry \[[IANA.well-known](#IANA.well-known)].[¶](#section-8.3-1)

#### [8.3.1.](#section-8.3.1) [Registry Contents](#name-registry-contents-2)

URI Suffix:

`oauth-protected-resource`[¶](#section-8.3.1-1.2)

Reference:

[Section 3](#PRConfig) of RFC 9728[¶](#section-8.3.1-1.4)

Status:

permanent[¶](#section-8.3.1-1.6)

Change Controller:

IETF[¶](#section-8.3.1-1.8)

Related Information:

(none)[¶](#section-8.3.1-1.10)

## [9.](#section-9) [References](#name-references)

### [9.1.](#section-9.1) [Normative References](#name-normative-references)

\[BCP195]

Moriarty, K. and S. Farrell, "Deprecating TLS 1.0 and TLS 1.1", BCP 195, RFC 8996, DOI 10.17487/RFC8996, March 2021, &lt;[https://www.rfc-editor.org/info/rfc8996](https://www.rfc-editor.org/info/rfc8996)&gt;.

Sheffer, Y., Saint-Andre, P., and T. Fossati, "Recommendations for Secure Use of Transport Layer Security (TLS) and Datagram Transport Layer Security (DTLS)", BCP 195, RFC 9325, DOI 10.17487/RFC9325, November 2022, &lt;[https://www.rfc-editor.org/info/rfc9325](https://www.rfc-editor.org/info/rfc9325)&gt;.

\[BCP47]

Phillips, A., Ed. and M. Davis, Ed., "Matching of Language Tags", BCP 47, RFC 4647, DOI 10.17487/RFC4647, September 2006, &lt;[https://www.rfc-editor.org/info/rfc4647](https://www.rfc-editor.org/info/rfc4647)&gt;.

Phillips, A., Ed. and M. Davis, Ed., "Tags for Identifying Languages", BCP 47, RFC 5646, DOI 10.17487/RFC5646, September 2009, &lt;[https://www.rfc-editor.org/info/rfc5646](https://www.rfc-editor.org/info/rfc5646)&gt;.

\[IANA.Language]

IANA, "Language Subtag Registry", &lt;[https://www.iana.org/assignments/language-subtag-registry](https://www.iana.org/assignments/language-subtag-registry)&gt;.

\[JWA]

Jones, M., "JSON Web Algorithms (JWA)", RFC 7518, DOI 10.17487/RFC7518, May 2015, &lt;[https://www.rfc-editor.org/info/rfc7518](https://www.rfc-editor.org/info/rfc7518)&gt;.

\[JWE]

Jones, M. and J. Hildebrand, "JSON Web Encryption (JWE)", RFC 7516, DOI 10.17487/RFC7516, May 2015, &lt;[https://www.rfc-editor.org/info/rfc7516](https://www.rfc-editor.org/info/rfc7516)&gt;.

\[JWK]

Jones, M., "JSON Web Key (JWK)", RFC 7517, DOI 10.17487/RFC7517, May 2015, &lt;[https://www.rfc-editor.org/info/rfc7517](https://www.rfc-editor.org/info/rfc7517)&gt;.

\[JWS]

Jones, M., Bradley, J., and N. Sakimura, "JSON Web Signature (JWS)", RFC 7515, DOI 10.17487/RFC7515, May 2015, &lt;[https://www.rfc-editor.org/info/rfc7515](https://www.rfc-editor.org/info/rfc7515)&gt;.

\[JWT]

Jones, M., Bradley, J., and N. Sakimura, "JSON Web Token (JWT)", RFC 7519, DOI 10.17487/RFC7519, May 2015, &lt;[https://www.rfc-editor.org/info/rfc7519](https://www.rfc-editor.org/info/rfc7519)&gt;.

\[RFC2119]

Bradner, S., "Key words for use in RFCs to Indicate Requirement Levels", BCP 14, RFC 2119, DOI 10.17487/RFC2119, March 1997, &lt;[https://www.rfc-editor.org/info/rfc2119](https://www.rfc-editor.org/info/rfc2119)&gt;.

\[RFC6749]

Hardt, D., Ed., "The OAuth 2.0 Authorization Framework", RFC 6749, DOI 10.17487/RFC6749, October 2012, &lt;[https://www.rfc-editor.org/info/rfc6749](https://www.rfc-editor.org/info/rfc6749)&gt;.

\[RFC6750]

Jones, M. and D. Hardt, "The OAuth 2.0 Authorization Framework: Bearer Token Usage", RFC 6750, DOI 10.17487/RFC6750, October 2012, &lt;[https://www.rfc-editor.org/info/rfc6750](https://www.rfc-editor.org/info/rfc6750)&gt;.

\[RFC7591]

Richer, J., Ed., Jones, M., Bradley, J., Machulak, M., and P. Hunt, "OAuth 2.0 Dynamic Client Registration Protocol", RFC 7591, DOI 10.17487/RFC7591, July 2015, &lt;[https://www.rfc-editor.org/info/rfc7591](https://www.rfc-editor.org/info/rfc7591)&gt;.

\[RFC8126]

Cotton, M., Leiba, B., and T. Narten, "Guidelines for Writing an IANA Considerations Section in RFCs", BCP 26, RFC 8126, DOI 10.17487/RFC8126, June 2017, &lt;[https://www.rfc-editor.org/info/rfc8126](https://www.rfc-editor.org/info/rfc8126)&gt;.

\[RFC8174]

Leiba, B., "Ambiguity of Uppercase vs Lowercase in RFC 2119 Key Words", BCP 14, RFC 8174, DOI 10.17487/RFC8174, May 2017, &lt;[https://www.rfc-editor.org/info/rfc8174](https://www.rfc-editor.org/info/rfc8174)&gt;.

\[RFC8259]

Bray, T., Ed., "The JavaScript Object Notation (JSON) Data Interchange Format", STD 90, RFC 8259, DOI 10.17487/RFC8259, December 2017, &lt;[https://www.rfc-editor.org/info/rfc8259](https://www.rfc-editor.org/info/rfc8259)&gt;.

\[RFC8414]

Jones, M., Sakimura, N., and J. Bradley, "OAuth 2.0 Authorization Server Metadata", RFC 8414, DOI 10.17487/RFC8414, June 2018, &lt;[https://www.rfc-editor.org/info/rfc8414](https://www.rfc-editor.org/info/rfc8414)&gt;.

\[RFC8615]

Nottingham, M., "Well-Known Uniform Resource Identifiers (URIs)", RFC 8615, DOI 10.17487/RFC8615, May 2019, &lt;[https://www.rfc-editor.org/info/rfc8615](https://www.rfc-editor.org/info/rfc8615)&gt;.

\[RFC8705]

Campbell, B., Bradley, J., Sakimura, N., and T. Lodderstedt, "OAuth 2.0 Mutual-TLS Client Authentication and Certificate-Bound Access Tokens", RFC 8705, DOI 10.17487/RFC8705, February 2020, &lt;[https://www.rfc-editor.org/info/rfc8705](https://www.rfc-editor.org/info/rfc8705)&gt;.

\[RFC8707]

Campbell, B., Bradley, J., and H. Tschofenig, "Resource Indicators for OAuth 2.0", RFC 8707, DOI 10.17487/RFC8707, February 2020, &lt;[https://www.rfc-editor.org/info/rfc8707](https://www.rfc-editor.org/info/rfc8707)&gt;.

\[RFC9110]

Fielding, R., Ed., Nottingham, M., Ed., and J. Reschke, Ed., "HTTP Semantics", STD 97, RFC 9110, DOI 10.17487/RFC9110, June 2022, &lt;[https://www.rfc-editor.org/info/rfc9110](https://www.rfc-editor.org/info/rfc9110)&gt;.

\[RFC9111]

Fielding, R., Ed., Nottingham, M., Ed., and J. Reschke, Ed., "HTTP Caching", STD 98, RFC 9111, DOI 10.17487/RFC9111, June 2022, &lt;[https://www.rfc-editor.org/info/rfc9111](https://www.rfc-editor.org/info/rfc9111)&gt;.

\[RFC9396]

Lodderstedt, T., Richer, J., and B. Campbell, "OAuth 2.0 Rich Authorization Requests", RFC 9396, DOI 10.17487/RFC9396, May 2023, &lt;[https://www.rfc-editor.org/info/rfc9396](https://www.rfc-editor.org/info/rfc9396)&gt;.

\[RFC9449]

Fett, D., Campbell, B., Bradley, J., Lodderstedt, T., Jones, M., and D. Waite, "OAuth 2.0 Demonstrating Proof of Possession (DPoP)", RFC 9449, DOI 10.17487/RFC9449, September 2023, &lt;[https://www.rfc-editor.org/info/rfc9449](https://www.rfc-editor.org/info/rfc9449)&gt;.

\[RFC9525]

Saint-Andre, P. and R. Salz, "Service Identity in TLS", RFC 9525, DOI 10.17487/RFC9525, November 2023, &lt;[https://www.rfc-editor.org/info/rfc9525](https://www.rfc-editor.org/info/rfc9525)&gt;.

\[UNICODE]

The Unicode Consortium, "The Unicode Standard", &lt;[https://www.unicode.org/versions/latest/](https://www.unicode.org/versions/latest/)&gt;.

\[USA15]

Whistler, K., Ed., "Unicode Normalization Forms", Unicode Standard Annex #15, 14 August 2024, &lt;[https://www.unicode.org/reports/tr15/](https://www.unicode.org/reports/tr15/)&gt;.

### [9.2.](#section-9.2) [Informative References](#name-informative-references)

\[FAPI.MessageSigning]

Tonge, D. and D. Fett, "FAPI 2.0 Message Signing (Draft)", 24 March 2023, &lt;[https://openid.net/specs/fapi-2\_0-message-signing.html](https://openid.net/specs/fapi-2_0-message-signing.html)&gt;.

\[IANA.JOSE]

IANA, "JSON Web Signature and Encryption Algorithms", &lt;[https://www.iana.org/assignments/jose](https://www.iana.org/assignments/jose)&gt;.

\[IANA.well-known]

IANA, "Well-Known URIs", &lt;[https://www.iana.org/assignments/well-known-uris](https://www.iana.org/assignments/well-known-uris)&gt;.

\[OpenID.Discovery]

Sakimura, N., Bradley, J., Jones, M., and E. Jay, "OpenID Connect Discovery 1.0 incorporating errata set 2", 15 December 2023, &lt;[https://openid.net/specs/openid-connect-discovery-1\_0.html](https://openid.net/specs/openid-connect-discovery-1_0.html)&gt;.

\[OWASP.SSRF]

OWASP Foundation, "OWASP Server-Side Request Forgery Prevention Cheat Sheet", &lt;[https://cheatsheetseries.owasp.org/cheatsheets/Server\_Side\_Request\_Forgery\_Prevention\_Cheat\_Sheet.html](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html)&gt;.

\[RFC7033]

Jones, P., Salgueiro, G., Jones, M., and J. Smarr, "WebFinger", RFC 7033, DOI 10.17487/RFC7033, September 2013, &lt;[https://www.rfc-editor.org/info/rfc7033](https://www.rfc-editor.org/info/rfc7033)&gt;.

\[RFC8620]

Jenkins, N. and C. Newman, "The JSON Meta Application Protocol (JMAP)", RFC 8620, DOI 10.17487/RFC8620, July 2019, &lt;[https://www.rfc-editor.org/info/rfc8620](https://www.rfc-editor.org/info/rfc8620)&gt;.

\[RFC9470]

Bertocci, V. and B. Campbell, "OAuth 2.0 Step Up Authentication Challenge Protocol", RFC 9470, DOI 10.17487/RFC9470, September 2023, &lt;[https://www.rfc-editor.org/info/rfc9470](https://www.rfc-editor.org/info/rfc9470)&gt;.

\[RFC9700]

Lodderstedt, T., Bradley, J., Labunets, A., and D. Fett, "Best Current Practice for OAuth 2.0 Security", BCP 240, RFC 9700, DOI 10.17487/RFC9700, January 2025, &lt;[https://www.rfc-editor.org/info/rfc9700](https://www.rfc-editor.org/info/rfc9700)&gt;.

## [Acknowledgements](#name-acknowledgements)

The authors of this specification would like to thank the attendees of the IETF 115 OAuth and HTTP API Working Group meetings and the attendees of subsequent OAuth Working Group meetings for their input on this specification. We would also like to thank Amanda Baber, Mike Bishop, Ralph Bragg, Brian Campbell, Deb Cooley, Gabriel Corona, Roman Danyliw, Vladimir Dzhuvinov, George Fletcher, Arnt Gulbrandsen, Pieter Kasselman, Murray Kucherawy, David Mandelberg, Tony Nadalin, Francesca Palombini, John Scudder, Rifaat Shekh-Yusef, Filip Skokan, Orie Steele, Atul Tulshibagwale, Éric Vyncke, Paul Wouters, and Bo Wu for their contributions to the specification.[¶](#appendix-A-1)

## [Authors' Addresses](#name-authors-addresses)

Michael B. Jones

Self-Issued Consulting

Phil Hunt

Independent Identity, Inc.

Aaron Parecki

Okta